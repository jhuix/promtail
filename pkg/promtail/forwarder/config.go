package forwarder

import (
	"flag"
	"time"
)

type ServerConfig struct {
	ForwardListenAddress    string        `yaml:"listen_address"`
	ForwardListenPort       int           `yaml:"listen_port"`
	ForwardConnLimit        int           `yaml:"listen_conn_limit"`
	ForwardReadTimeout      time.Duration `yaml:"read_timeout"`
	ForwardWriteTimeout     time.Duration `yaml:"write_timeout"`
	ForwardKeepalivePeriod  time.Duration `yaml:"keepalive_period"`
	ForwardKeepaliveTimeout time.Duration `yaml:"keepalive_timeout"`
	ForwardClientConfig     ConnConfig    `yaml:"conn_config,omitempty"`
	ForwardTLSConfig        TLSConfig     `yaml:"tls_config,omitempty"`
}

// RegisterFlagsWithPrefix with prefix registers flags where every name is prefixed by
// prefix. If prefix is a non-empty string, prefix should end with a period.
func (cfg *ServerConfig) RegisterFlagsWithPrefix(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.ForwardListenAddress, prefix+"forwarder.listen-address", "", "Forward server listen address.")
	f.IntVar(&cfg.ForwardListenPort, prefix+"forwarder.listen-port", 9081, "Forward server listen port.")
	f.IntVar(&cfg.ForwardConnLimit, prefix+"forwarder.conn-limit", 1, "Maximum number of simultaneous forward connections, <=0 to disable")
	f.DurationVar(&cfg.ForwardReadTimeout, prefix+"forwarder.read-timeout", 30*time.Second, "Read timeout for forward server")
	f.DurationVar(&cfg.ForwardWriteTimeout, prefix+"forwarder.write-timeout", 30*time.Second, "Write timeout for forward server")
	f.DurationVar(&cfg.ForwardKeepalivePeriod, prefix+"forwarder.keepalive.period", time.Minute*3, "Duration after which a keepalive probe is sent in case of no activity over the connection., Default: 3m")
	f.DurationVar(&cfg.ForwardKeepaliveTimeout, prefix+"forwarder.keepalive.timeout", time.Second*20, "After having pinged for keepalive check, the duration after which an idle connection should be closed, Default: 20s")
	f.StringVar(&cfg.ForwardTLSConfig.TLSCertPath, prefix+"forwarder.tls-cert-path", "", "Forward TLS server cert path.")
	f.StringVar(&cfg.ForwardTLSConfig.TLSKeyPath, prefix+"forwarder.tls-key-path", "", "Forward TLS server key path.")
}

// RegisterFlags adds the flags required to config this to the given FlagSet
func (cfg *ServerConfig) RegisterFlags(f *flag.FlagSet) {
	cfg.RegisterFlagsWithPrefix("", f)
}
