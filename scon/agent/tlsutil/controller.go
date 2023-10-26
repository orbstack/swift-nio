package tlsutil

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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

	certImportTimeout = 10 * time.Second
)

type TLSController struct {
	certsLRU *lru.Cache[string, *tls.Certificate]

	rootCert *x509.Certificate
	rootKey  crypto.PrivateKey

	// set in docker.go init
	host *hclient.Client

	firstConnDone atomic.Bool
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

func (t *TLSController) LoadRoot() error {
	// TODO move to thread in case it blocks somehow
	certData, err := t.host.GetTLSRootData()
	if err != nil {
		return fmt.Errorf("get root data: %w", err)
	}

	// load root cert
	rootCert, rootKey, err := LoadRoot([]byte(certData.CertPEM), []byte(certData.KeyPEM))
	if err != nil {
		return fmt.Errorf("load root: %w", err)
	}
	t.rootCert = rootCert
	t.rootKey = rootKey

	return nil
}

func (t *TLSController) MakeCertForHost(hostname string) (*tls.Certificate, error) {
	if cert, ok := t.certsLRU.Get(hostname); ok {
		return cert, nil
	}

	// cert generation is fast (~3 ms) but still cache in LRU for consistent cert identity and minor optimization
	cert, err := GenerateCert(t.rootCert, t.rootKey, hostname)
	if err != nil {
		return nil, fmt.Errorf("generate cert: %w", err)
	}

	// add to LRU immediately
	t.certsLRU.Add(hostname, cert)

	// now, if this is the first connection, we may want to hang for a bit (below browser timeout, i.e. up to 10 sec) and import cert to system keychain and ask for trust settings
	if !t.firstConnDone.Swap(true) {
		err := util.WithTimeout1(func() error {
			return t.host.ImportCertificate(base64.StdEncoding.EncodeToString(t.rootCert.Raw))
		}, certImportTimeout)
		if err != nil {
			logrus.WithError(err).Error("failed to import certificate")
		}
	}

	return cert, nil
}
