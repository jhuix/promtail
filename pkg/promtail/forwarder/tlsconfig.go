package forwarder

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io/ioutil"
)

var ErrParseRootCertFailed = errors.New("failed to parse root certificate")

type TLSConfig struct {
	TLSCertPath string `yaml:"cert_file"`
	TLSKeyPath  string `yaml:"key_file"`
	//	ClientAuth  string `yaml:"client_auth_type"`
	//	ClientCAs   string `yaml:"client_ca_file"`
}

func tlsBytes(cert, key string) (certBytes, keyBytes []byte, err error) {
	certBytes, err = ioutil.ReadFile(cert)
	if err != nil {
		return
	}

	keyBytes, err = ioutil.ReadFile(key)
	return
}

func getClientTlsConfig(certBytes, keyBytes []byte) (conf *tls.Config, err error) {
	var cert tls.Certificate
	cert, err = tls.X509KeyPair(certBytes, keyBytes)
	if err != nil {
		return
	}
	serverCertPool := x509.NewCertPool()
	ok := serverCertPool.AppendCertsFromPEM(certBytes)
	if !ok {
		err = ErrParseRootCertFailed
	}
	conf = &tls.Config{
		RootCAs:            serverCertPool,
		ServerName:         "promtail",
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}
	return
}
func getServerTlsConfig(certBytes, keyBytes []byte) (conf *tls.Config, err error) {
	var cert tls.Certificate
	cert, err = tls.X509KeyPair(certBytes, keyBytes)
	if err != nil {
		return
	}

	clientCertPool := x509.NewCertPool()
	ok := clientCertPool.AppendCertsFromPEM(certBytes)
	if !ok {
		err = ErrParseRootCertFailed
	}

	conf = &tls.Config{
		ClientCAs:    clientCertPool,
		ServerName:   "promtail",
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	return
}
