package forwarder

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cortexproject/cortex/pkg/util"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"github.com/grafana/loki/pkg/promtail/api"
)

var (
	ErrNoTlsConfig = errors.New("tls config is not exist")
)

type EntryItem struct {
	conn   *Conn
	closed bool
}

type Server struct {
	entries    chan api.Entry
	logger     log.Logger
	cfg        ServerConfig
	ln         net.Listener
	activeConn map[*Conn]struct{}
	connChan   chan EntryItem
	ctx        context.Context
	ctxCancel  context.CancelFunc
	wg         sync.WaitGroup
	once       sync.Once
}

// New makes a new Server
func New(logger log.Logger, cfg ServerConfig) (*Server, error) {
	address := fmt.Sprintf("%v:%v", cfg.ForwardListenAddress, cfg.ForwardListenPort)
	ctx, cancel := context.WithCancel(context.Background())
	if cfg.ForwardClientConfig.ReadTimeout == 0 {
		cfg.ForwardClientConfig.ReadTimeout = cfg.ForwardServerReadTimeout
	}
	if cfg.ForwardClientConfig.WriteTimeout == 0 {
		cfg.ForwardClientConfig.WriteTimeout = cfg.ForwardServerWriteTimeout
	}
	srv := &Server{
		logger:     log.With(logger, "component", "forwarder", "host", address),
		cfg:        cfg,
		entries:    make(chan api.Entry),
		activeConn: make(map[*Conn]struct{}),
		connChan:   make(chan EntryItem, 1),
		ctx:        ctx,
		ctxCancel:  cancel,
	}
	err := srv.Start()
	return srv, err
}

// Chan implements Server
func (s *Server) Chan() chan<- api.Entry {
	return s.entries
}

// Stop implements Server
func (s *Server) Stop() {
	s.once.Do(func() {
		s.ctxCancel()
		if s.ln != nil {
			_ = s.ln.Close()
			s.ln = nil
		}
		close(s.entries)
		close(s.connChan)
	})
	s.wg.Wait()
	for c := range s.activeConn {
		c.Stop()
		delete(s.activeConn, c)
	}
}

// StopNow implements Server
func (s *Server) StopNow() {
	s.Stop()
}

func (s *Server) listenTCP() (err error) {
	address := fmt.Sprintf("%s:%d", s.cfg.ForwardListenAddress, s.cfg.ForwardListenPort)
	s.ln, err = net.Listen("tcp", address)
	return
}

func (s *Server) listenTLS() error {
	if len(s.cfg.ForwardTLSConfig.TLSCertPath) == 0 || len(s.cfg.ForwardTLSConfig.TLSKeyPath) == 0 {
		return ErrNoTlsConfig
	}

	certBytes, keyBytes, err := tlsBytes(s.cfg.ForwardTLSConfig.TLSCertPath, s.cfg.ForwardTLSConfig.TLSKeyPath)
	if err != nil {
		return err
	}

	var conf *tls.Config
	conf, err = getServerTlsConfig(certBytes, keyBytes)
	if err != nil {
		return err
	}

	address := fmt.Sprintf("%s:%d", s.cfg.ForwardListenAddress, s.cfg.ForwardListenPort)
	s.ln, err = tls.Listen("tcp", address, conf)
	return err
}

func (s *Server) handleCloseConnection(c *Conn) {
	s.connChan <- EntryItem{
		conn:   c,
		closed: true,
	}
}

func (s *Server) serverListen() error {
	if err := s.listenTLS(); err != nil {
		if err = s.listenTCP(); err != nil {
			return err
		}
	}

	level.Info(s.logger).Log("msg", "forward server listening on addresses")

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		backoff := util.NewBackoff(s.ctx, util.BackoffConfig{
			MinBackoff: 5 * time.Millisecond,
			MaxBackoff: 1 * time.Second,
		})

		for {
			conn, e := s.ln.Accept()
			if e != nil {
				if s.ctx.Err() != nil {
					level.Info(s.logger).Log("msg", "forward server shutting down")
					return
				}

				if ne, ok := e.(net.Error); ok && ne.Temporary() {
					level.Warn(s.logger).Log("msg", "failed to accept forward connection", "err", e, "num_retries", backoff.NumRetries())
					backoff.Wait()
					continue
				}

				if strings.Contains(e.Error(), "listener closed") {
					level.Error(s.logger).Log("msg", "forward server closed", "error", e)
					return
				}

				level.Error(s.logger).Log("msg", "failed to accept forward connection", "error", e)
				return
			}
			backoff.Reset()

			if tc, ok := conn.(*net.TCPConn); ok {
				_ = tc.SetKeepAlive(true)
				_ = tc.SetKeepAlivePeriod(s.cfg.ForwardKeepalivePeriod)
				_ = tc.SetLinger(10)
			}

			go func() {
				if tlsConn, ok := conn.(*tls.Conn); ok {
					if s.cfg.ForwardClientConfig.ReadTimeout != 0 {
						_ = conn.SetReadDeadline(time.Now().Add(s.cfg.ForwardClientConfig.ReadTimeout))
					}
					if s.cfg.ForwardClientConfig.WriteTimeout != 0 {
						_ = conn.SetWriteDeadline(time.Now().Add(s.cfg.ForwardClientConfig.WriteTimeout))
					}
					if err := tlsConn.Handshake(); err != nil {
						level.Error(s.logger).Log("msg", fmt.Sprintf("TLS handshake error from %s", conn.RemoteAddr()), "error", err)
						_ = conn.Close()
						return
					}
				}

				s.connChan <- EntryItem{
					conn:   NewConn(s.cfg.ForwardClientConfig, s.logger, conn, s.handleCloseConnection),
					closed: false,
				}
			}()
		}
	}()
	return nil
}

func (s *Server) Start() error {
	if err := s.serverListen(); err != nil {
		return err
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		for {
			for len(s.activeConn) == 0 {
				select {
				case item, ok := <-s.connChan:
					if !ok {
						return
					}

					if item.conn == nil {
						continue
					}

					if item.closed {
						delete(s.activeConn, item.conn)
						continue
					}

					if s.cfg.ForwardConnLimit > 0 && len(s.activeConn) >= s.cfg.ForwardConnLimit {
						level.Warn(s.logger).Log("msg", fmt.Sprintf("connection (%s) rejected because forward server limit connections is %d", item.conn.RemoteAddr().String(), s.cfg.ForwardConnLimit))
						_ = item.conn.Close()
						continue
					}

					s.activeConn[item.conn] = struct{}{}
					item.conn.Start()
				}
			}

			select {
			case e, ok := <-s.entries:
				if !ok {
					return
				}

				for c := range s.activeConn {
					if err := c.SendEntry(e); err != nil {
						delete(s.activeConn, c)
						c.Stop()
					}
				}
			case item, ok := <-s.connChan:
				if !ok {
					return
				}

				if item.conn == nil {
					continue
				}

				if item.closed {
					delete(s.activeConn, item.conn)
					continue
				}

				if s.cfg.ForwardConnLimit > 0 && len(s.activeConn) >= s.cfg.ForwardConnLimit {
					_ = item.conn.Close()
					continue
				}

				s.activeConn[item.conn] = struct{}{}
				item.conn.Start()
			}
		}
	}()
	return nil
}
