package forwarder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"

	"github.com/grafana/loki/pkg/logproto"
	"github.com/grafana/loki/pkg/promtail/api"
)

type ConnConfig struct {
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	// The tenant ID to use when pushing logs to Loki (empty string means
	// single tenant mode)
	TenantID string `yaml:"tenant_id"`
}

type Conn struct {
	net.Conn
	cfg         ConnConfig
	logger      log.Logger
	wg          sync.WaitGroup
	handleClose func(*Conn)
}

func NewConn(cfg ConnConfig, logger log.Logger, conn net.Conn, handleClose func(*Conn)) *Conn {
	c := &Conn{
		cfg:         cfg,
		Conn:        conn,
		logger:      log.With(logger, "component", "forward", "host", conn.RemoteAddr().String()),
		handleClose: handleClose,
	}
	return c
}

// Stop the Conn.
func (c *Conn) Stop() {
	_ = c.Close()
	c.wg.Wait()
}

func (c *Conn) Start() {
	c.wg.Add(1)
	go c.run()
}

func (c *Conn) run() {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			ss := runtime.Stack(buf, false)
			if ss > size {
				ss = size
			}
			buf = buf[:ss]
			level.Error(c.logger).Log("msg", fmt.Sprintf("connection %s panic", c.RemoteAddr().String()), "error", err, "stack", buf)
		}
		c.wg.Done()
		_ = c.Close()
		level.Info(c.logger).Log("msg", "forward connection closed")
		if c.handleClose != nil {
			c.handleClose(c)
		}
	}()

	level.Info(c.logger).Log("msg", "forward connection running")

	for {
		if c.cfg.ReadTimeout != 0 {
			_ = c.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
		}
		header := [4]byte{}
		_, err := io.ReadFull(c, header[:4])
		if err != nil {
			level.Error(c.logger).Log("msg", "connection is closed or failed to read data", "error", err)
			break
		}

		if (header[0] == 'p' || header[0] == 'P') &&
			(header[1] == 'i' || header[1] == 'I') &&
			(header[2] == 'n' || header[2] == 'N') &&
			(header[3] == 'g' || header[3] == 'G') {
			if header[1] == 'i' {
				header[1] = 'o'
			} else if header[1] == 'I' {
				header[1] = 'O'
			}
			if c.cfg.WriteTimeout != 0 {
				_ = c.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout))
			}
			c.Write(header[:4])
			continue
		}

		level.Warn(c.logger).Log("msg", "close connection, read data is invalid")
		break
	}
}

func (c *Conn) SendEntry(entry api.Entry) error {
	buf, _, err := c.encode(entry)
	if err != nil {
		level.Error(c.logger).Log("msg", "error encoding entry", "error", err)
		return err
	}

	if c.cfg.WriteTimeout != 0 {
		_ = c.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout))
	}
	if n, err := c.Write(buf); err != nil {
		level.Error(c.logger).Log("msg", fmt.Sprintf("error write entry %d", n), "error", err)
		return err
	}

	return nil
}

// encode the batch as snappy-compressed push request, and returns
// the encoded bytes and the number of encoded entries
func (c *Conn) encode(entry api.Entry) ([]byte, int, error) {
	req, entriesCount := c.createPushRequest(entry)
	buf, err := proto.Marshal(req)
	if err != nil {
		return nil, 0, err
	}

	buf = snappy.Encode(nil, buf)
	data := bytes.Buffer{}
	size := uint32(len(buf))
	binary.Write(&data, binary.LittleEndian, size)
	binary.Write(&data, binary.LittleEndian, buf)
	return data.Bytes(), entriesCount, nil
}

// creates push request and returns it, together with number of entries
func (c *Conn) createPushRequest(entry api.Entry) (*logproto.PushRequest, int) {
	req := logproto.PushRequest{
		Streams: make([]logproto.Stream, 0, 1),
	}

	stream := &logproto.Stream{
		Labels:  entry.Labels.String(),
		Entries: []logproto.Entry{entry.Entry},
	}
	req.Streams = append(req.Streams, *stream)
	entriesCount := len(stream.Entries)
	return &req, entriesCount
}
