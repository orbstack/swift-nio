package domainproxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/sirupsen/logrus"
)

const (
	// fast to generate
	certsLRUSize = 100

	// must match maxCertDismissCount in Swift
	maxCertDismissCount = 2
	certImportTimeout   = 10 * time.Second
)

type TLSController struct {
	certsLRU *lru.Cache[string, *tls.Certificate]

	// set in docker.go init
	host *hclient.Client

	connCount atomic.Uint64
}

func NewTLSController(host *hclient.Client) (*TLSController, error) {
	certsLRU, err := lru.New[string, *tls.Certificate](certsLRUSize)
	if err != nil {
		return nil, err
	}

	return &TLSController{
		certsLRU: certsLRU,
		host:     host,
	}, nil
}

func (t *TLSController) GetCertForHost(hostname string) (*tls.Certificate, error) {
	if cert, ok := t.certsLRU.Get(hostname); ok {
		return cert, nil
	}

	// cert generation is fast (~3 ms) but still cache in LRU for consistent cert identity and minor optimization
	hCert, err := t.host.GenerateTLSCertificate(hostname)
	if err != nil {
		return nil, fmt.Errorf("generate cert: %w", err)
	}
	privateKey, err := x509.ParseECPrivateKey(hCert.PrivateKeyDER)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	cert := &tls.Certificate{
		Certificate: hCert.Certificate,
		PrivateKey:  privateKey,
	}

	// add to LRU immediately
	t.certsLRU.Add(hostname, cert)

	// now, if this is the first OR second connection, we may want to hang for a bit (below browser timeout, i.e. up to 10 sec) and import cert to system keychain and ask for trust settings
	// TODO: browser could issue multiple conns in a short span of time, but risk is negligible. hard to accidentally decline anyway.
	if t.connCount.Add(1) <= maxCertDismissCount {
		err := util.WithTimeout1(func() error {
			return t.host.ImportTLSCertificate()
		}, certImportTimeout)
		if err != nil {
			logrus.WithError(err).Error("failed to import certificate")
		}
	}

	return cert, nil
}
