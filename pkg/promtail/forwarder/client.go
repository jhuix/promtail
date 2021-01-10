package forwarder

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"

	"github.com/grafana/loki/pkg/logproto"
)

const (
	// ReaderBuffSize is used for bufio reader.
	ReaderBuffSize           = 16 * 1024 // 16K bytes
	DefaultIdleTimeout       = 30 * time.Second
	DefaultConnectTimeout    = 3 * time.Second
	DefaultHeartbeatInterval = 20 * time.Second
)

type ClientConfig struct {
	Host              string        `yaml:"host"`
	Port              int           `yaml:"port"`
	MaxMessageLength  int           `yaml:"max_message_length"`
	ConnectTimeout    time.Duration `yaml:"connect_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	KeepalivePeriod   time.Duration `yaml:"keepalive_period"` // If it is zero we don't set keepalive
	HeartbeatInterval time.Duration `yaml:"heart_interval"`
	ReconnectInterval time.Duration `yaml:"reconnect_interval"` // If it is zero we don't reconnect
	// TLSConfig to use to connect to the targets.
	TLSConfig TLSConfig `yaml:"tls_config,omitempty"`
}

type Client struct {
	conn                net.Conn
	logger              log.Logger
	cfg                 ClientConfig
	handleServerRequest func(req *logproto.PushRequest)
	wg                  sync.WaitGroup
	mu                  sync.Mutex // protects following
	shutdown            bool
}

func NewClient(logger log.Logger, cfg ClientConfig, handler func(req *logproto.PushRequest)) *Client {
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = DefaultConnectTimeout
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.MaxMessageLength == 0 {
		cfg.MaxMessageLength = ReaderBuffSize
	}
	clt := &Client{
		logger:              log.With(logger, "component", "forward", "host", fmt.Sprintf("%v:%v", cfg.Host, cfg.Port)),
		cfg:                 cfg,
		handleServerRequest: handler,
	}
	return clt
}

// IsShutdown client is shutdown or not.
func (c *Client) IsShutdown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shutdown
}

func (c *Client) TlsConnect() error {
	certBytes, keyBytes, err := tlsBytes(c.cfg.TLSConfig.TLSCertPath, c.cfg.TLSConfig.TLSKeyPath)
	if err != nil {
		return err
	}

	var conf *tls.Config
	conf, err = getClientTlsConfig(certBytes, keyBytes)
	if err != nil {
		return err
	}

	address := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	dialer := &net.Dialer{
		Timeout: c.cfg.ConnectTimeout,
	}
	var tlsConn *tls.Conn
	tlsConn, err = tls.DialWithDialer(dialer, "tcp", address, conf)
	if err != nil {
		return err
	}
	c.conn = net.Conn(tlsConn)
	//var conn net.Conn
	//conn, err = net.DialTimeout("tcp", address, c.cfg.ConnectTimeout)
	//if err != nil {
	//	return err
	//}
	//
	//c.conn = tls.Client(conn, conf)
	return nil
}

func (c *Client) ConnectTcp() (err error) {
	address := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	c.conn, err = net.DialTimeout("tcp", address, c.cfg.ConnectTimeout)
	return
}

func (c *Client) Connect() (err error) {
	if len(c.cfg.TLSConfig.TLSCertPath) == 0 || len(c.cfg.TLSConfig.TLSKeyPath) == 0 {
		return c.ConnectTcp()
	}

	return c.TlsConnect()
}

func (c *Client) run() {
	defer c.wg.Done()

	level.Info(c.logger).Log("msg", "forward client running")
	r := bufio.NewReaderSize(c.conn, ReaderBuffSize)
	buf := make([]byte, 512)
	var msg proto.Message
	req := &logproto.PushRequest{}
	msg = req
	for {
		if c.cfg.IdleTimeout != 0 {
			_ = c.conn.SetDeadline(time.Now().Add(c.cfg.IdleTimeout))
		}

		header := [4]byte{}
		_, err := io.ReadFull(r, header[:4])
		if err != nil {
			level.Error(c.logger).Log("msg", "failed to read log", "error", err)
			break
		}

		// Is heartbeat
		if (header[0] == 'p' || header[0] == 'P') &&
			(header[1] == 'o' || header[1] == 'O') &&
			(header[2] == 'n' || header[2] == 'N') &&
			(header[3] == 'g' || header[3] == 'G') {
			continue
		}

		l := binary.LittleEndian.Uint32(header[:4])
		if l > 0 && int(l) > c.cfg.MaxMessageLength {
			level.Warn(c.logger).Log("msg", fmt.Sprintf("log is too long: %d", l))
			break
		}

		size := int(l)
		if cap(buf) >= size {
			buf = buf[:size]
		} else {
			buf = make([]byte, size)
		}
		_, err = io.ReadFull(r, buf)
		if err != nil {
			level.Error(c.logger).Log("msg", "failed to read log", "error", err)
			break
		}

		// We re-implement proto.Unmarshal here as it calls XXX_Unmarshal first,
		// which we can't override without upsetting golint.
		msg.Reset()
		buf, _ = snappy.Decode(nil, buf)
		if u, ok := msg.(proto.Unmarshaler); ok {
			err = u.Unmarshal(buf)
		} else {
			err = proto.NewBuffer(buf).Unmarshal(msg)
		}
		if err != nil {
			level.Error(c.logger).Log("msg", "failed to parse log", "error", err)
			break
		}

		if c.handleServerRequest != nil {
			c.handleServerRequest(req)
		}
	}

	c.mu.Lock()
	c.shutdown = true
	c.mu.Unlock()
	_ = c.conn.Close()
	level.Info(c.logger).Log("msg", "forward client closed")

	// Is reconnect
	if c.cfg.ReconnectInterval != 0 {
		go func() {
			t := time.NewTimer(c.cfg.ReconnectInterval)
			select {
			case <-t.C:
				_ = c.Start()
			}
			t.Stop()
		}()
	}
}

func (c *Client) heartbeat() {
	defer c.wg.Done()

	level.Info(c.logger).Log("msg", "heartbeat of forward client running")
	t := time.NewTicker(c.cfg.HeartbeatInterval)
	for range t.C {
		if c.IsShutdown() {
			t.Stop()
			return
		}

		if _, err := c.conn.Write([]byte("ping")); err != nil {
			level.Warn(c.logger).Log("msg", fmt.Sprintf("failed to heartbeat to %s", c.conn.RemoteAddr().String()))
		}

		if c.cfg.IdleTimeout != 0 {
			_ = c.conn.SetDeadline(time.Now().Add(c.cfg.IdleTimeout))
		}
	}
}

func (c *Client) Start() error {
	if err := c.Connect(); err != nil {
		return err
	}

	if tc, ok := c.conn.(*net.TCPConn); ok && c.cfg.KeepalivePeriod > 0 {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(c.cfg.KeepalivePeriod)
	}
	if c.cfg.IdleTimeout != 0 {
		_ = c.conn.SetDeadline(time.Now().Add(c.cfg.IdleTimeout))
	}

	c.wg.Add(1)
	go c.run()

	if c.cfg.HeartbeatInterval > 0 {
		c.wg.Add(1)
		go c.heartbeat()
	}
	return nil
}

// Stop implements Client
func (c *Client) Stop() {
	c.cfg.ReconnectInterval = 0
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.wg.Wait()
}

// StopNow implements Client
func (c *Client) StopNow() {
	c.Stop()
}
