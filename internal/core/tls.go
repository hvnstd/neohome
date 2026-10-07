package core

// TLS subsystem: certificate authorities and issued certificates as real
// world state. The shape of TLSState is owned by this file; World only
// carries the pointer.
//
// Like World.SSHHostKey, certificates and keys are kept as DER / PKCS8
// bytes so gob can carry the world, and validity is judged against the
// simulated clock (World.Sim), never the wall clock.

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// TLSCert is one certificate in the world: either a self-signed root CA
// (IsCA, Issuer "") or a leaf issued by a named CA. Only raw bytes are
// stored; parse on demand.
type TLSCert struct {
	Name      string // CA name; for leaves the label (== CN)
	IsCA      bool
	Issuer    string // name of the CA that signed it ("" for a root)
	CertDER   []byte
	KeyDER    []byte // PKCS#8
	NotBefore time.Time
	NotAfter  time.Time
}

// TLSState holds every CA the world knows and every certificate it issued.
type TLSState struct {
	CAs    map[string]*TLSCert
	Issued []*TLSCert
}

// TLS returns the world's TLS subsystem, creating it on first use. Called
// from world_init (seedTLS) so a fresh world already has a root CA.
func (w *World) TLSState() *TLSState {
	if w.TLS == nil {
		w.TLS = &TLSState{CAs: map[string]*TLSCert{}}
	}
	return w.TLS
}

// GenerateCA creates a self-signed root CA named name, valid for ten
// simulated years from now. The world keeps it under its name; issuing a
// second CA with the same name is refused.
func (w *World) GenerateCA(name string) (*TLSCert, error) {
	s := w.TLSState()
	if name == "" {
		return nil, fmt.Errorf("tls: CA needs a name")
	}
	if _, dup := s.CAs[name]; dup {
		return nil, fmt.Errorf("tls: CA %q already exists", name)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	now := w.Now()
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name, Organization: []string{"neohome"}},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	ca := &TLSCert{
		Name:      name,
		IsCA:      true,
		CertDER:   der,
		KeyDER:    pkcs8(key),
		NotBefore: tpl.NotBefore,
		NotAfter:  tpl.NotAfter,
	}
	s.CAs[name] = ca
	w.AddEvent("world", "info", "tls", "created CA %q", name)
	return ca, nil
}

// IssueCert issues a leaf certificate for domain, signed by ca. The leaf is
// valid for one simulated year and carries the domain as CN and DNS SAN.
// The CA must be the world's own CA object and must itself be valid now.
func (w *World) IssueCert(domain string, ca *TLSCert) (*TLSCert, error) {
	s := w.TLSState()
	if domain == "" {
		return nil, fmt.Errorf("tls: certificate needs a domain")
	}
	if ca == nil || !ca.IsCA {
		return nil, fmt.Errorf("tls: %q is not a CA", certLabel(ca))
	}
	known := s.CAs[ca.Name]
	if known != ca {
		return nil, fmt.Errorf("tls: CA %q is not part of this world", ca.Name)
	}
	now := w.Now()
	if now.Before(ca.NotBefore) || now.After(ca.NotAfter) {
		return nil, fmt.Errorf("tls: CA %q is not valid now", ca.Name)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	caPub, err := x509.ParseCertificate(ca.CertDER)
	if err != nil {
		return nil, err
	}
	caKey, err := x509.ParsePKCS8PrivateKey(ca.KeyDER)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain, Organization: []string{"neohome"}},
		DNSNames:     []string{domain},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caPub, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	leaf := &TLSCert{
		Name:      domain,
		Issuer:    ca.Name,
		CertDER:   der,
		KeyDER:    pkcs8(key),
		NotBefore: tpl.NotBefore,
		NotAfter:  tpl.NotAfter,
	}
	s.Issued = append(s.Issued, leaf)
	w.AddEvent("world", "info", "tls", "issued certificate for %s by CA %q", domain, ca.Name)
	return leaf, nil
}

// Verify checks cert against ca the way a real client would: chain to the
// CA, the CA is one of the world's roots, and the whole chain is inside its
// validity window at the current simulated time.
func (w *World) Verify(cert, ca *TLSCert) bool {
	if cert == nil || ca == nil || !ca.IsCA {
		return false
	}
	if s := w.TLS; s == nil || s.CAs[ca.Name] != ca {
		return false
	}
	caPub, err := x509.ParseCertificate(ca.CertDER)
	if err != nil {
		return false
	}
	leaf, err := x509.ParseCertificate(cert.CertDER)
	if err != nil {
		return false
	}
	pool := x509.NewCertPool()
	pool.AddCert(caPub)
	opts := x509.VerifyOptions{Roots: pool, CurrentTime: w.Now()}
	_, err = leaf.Verify(opts)
	return err == nil
}

// TLSError is a client-visible TLS failure with a curl-like category so the
// shell can report it the way a real client would.
type TLSError struct {
	Code int // 35 handshake failure, 60 certificate problem
	Msg  string
}

func (e *TLSError) Error() string { return e.Msg }

// Handshake performs the client side of a TLS session against svc as host,
// judged the way a real client judges it: the service must present a
// certificate for that host, its issuer must be a CA this world knows, the
// client device must trust that CA, and the chain must be inside its
// validity window at the current simulated time.
func (w *World) Handshake(client *Device, host string, svc *Service) (*TLSCert, *TLSCert, error) {
	fail := func(code int, format string, a ...any) (*TLSCert, *TLSCert, error) {
		return nil, nil, &TLSError{Code: code, Msg: fmt.Sprintf(format, a...)}
	}
	if svc == nil || svc.TLSCert == "" {
		return fail(35, "handshake failure: server did not present a certificate")
	}
	s := w.TLS
	if s == nil {
		return fail(35, "handshake failure: server did not present a certificate")
	}
	var leaf *TLSCert
	for _, c := range s.Issued {
		if c.Name == svc.TLSCert {
			leaf = c
			break
		}
	}
	if leaf == nil {
		return fail(35, "handshake failure: server certificate %q is not part of this world", svc.TLSCert)
	}
	crt, err := x509.ParseCertificate(leaf.CertDER)
	if err != nil {
		return fail(35, "handshake failure: server presented an unusable certificate")
	}
	if leaf.Name != host && !servesSAN(crt, host) {
		return fail(60, "no alternative certificate subject name matches target host name %q", host)
	}
	ca := s.CAs[leaf.Issuer]
	if ca == nil {
		return fail(60, "unable to get local issuer certificate")
	}
	if !w.deviceTrustsCA(client, ca) {
		return fail(60, "unable to get local issuer certificate (%q is not in the client trust store)", ca.Name)
	}
	if !w.Verify(leaf, ca) {
		return fail(60, "certificate has expired or is not yet valid")
	}
	return leaf, ca, nil
}

func servesSAN(crt *x509.Certificate, host string) bool {
	for _, n := range crt.DNSNames {
		if n == host {
			return true
		}
	}
	return false
}

// deviceTrustsCA reports whether the client device holds the CA's
// certificate as a PEM file under /etc/ssl/certs. Trust is files on the
// device, so removing or planting trust material really changes what the
// device trusts.
func (w *World) deviceTrustsCA(d *Device, ca *TLSCert) bool {
	if d == nil {
		return false
	}
	// FS.List returns full paths under the directory
	for _, p := range d.FS.List("/etc/ssl/certs") {
		data, ok := d.FS.Read(p)
		if !ok {
			continue
		}
		if pemHasCert(data, ca.CertDER) {
			return true
		}
	}
	return false
}

// pemHasCert reports whether the PEM bytes contain exactly this certificate.
func pemHasCert(data []byte, der []byte) bool {
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return false
		}
		if block.Type == "CERTIFICATE" && bytes.Equal(block.Bytes, der) {
			return true
		}
	}
}

// PEMCert / PEMKey render a certificate for the device filesystem, so the
// material a service would use is a real file a player can cat.
func PEMCert(c *TLSCert) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.CertDER}))
}

func PEMKey(c *TLSCert) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: c.KeyDER}))
}

// ParseCert rehydrates the x509 view of a stored certificate.
func ParseCert(c *TLSCert) (*x509.Certificate, error) {
	return x509.ParseCertificate(c.CertDER)
}

// ParsePEM rehydrates the x509 view from a PEM file on a device — the file
// is a view of world truth, and diagnostics read it back the way a real
// tool would.
func ParsePEM(data string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(data))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("no PEM certificate found")
	}
	return x509.ParseCertificate(block.Bytes)
}

func certLabel(c *TLSCert) string {
	if c == nil {
		return "(nil)"
	}
	return c.Name
}

func pkcs8(key *rsa.PrivateKey) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil
	}
	return der
}

// httpsHosts are the public names that speak TLS in the seeded world: a
// leaf is issued for each and carried by the serving service on port 443.
// httpsHosts are the names that speak TLS in the seeded world: public web
// hosts on 443 and the household's own mail hosts on 993.
var httpsHosts = []struct{ host, devID, svc string }{
	{"mirror.neohome.example", "mirror", "nginx"},
	{"api.novapanel.example", "prov-api", "nginx"},
	{"bank.firstneohome.example", "bank", "httpd"},
	{"jobs.hiring.example", "jobs", "httpd"},
	{"git.neohome.example", "git", "nginx"},
	{"home-pc", "pc-alex", "imaps"},
	{"assistant", "asst-alex", "imaps"},
}

// seedTLS gives the fresh world a root CA whose material lives on the
// assistant node as real files under /etc/ssl, issues the public hosts'
// certificates, and installs the household root into the trust stores a
// real client would consult. Called from world_init after the device graph
// exists.
func seedTLS(w *World) {
	ca, err := w.GenerateCA("neohome-root-ca")
	if err != nil {
		return // GenerateCA cannot fail on a fresh world; stay silent but honest
	}
	if asst := w.Devices["asst-alex"]; asst != nil {
		asst.FS.MkdirAll("/etc/ssl/certs", 0755, "root", "root")
		asst.FS.MkdirAll("/etc/ssl/private", 0700, "root", "root")
		asst.FS.Write("/etc/ssl/certs/neohome-root-ca.pem", PEMCert(ca), 0644, "root", "root")
		asst.FS.Write("/etc/ssl/private/neohome-root-ca.key", PEMKey(ca), 0600, "root", "root")
		asst.Logf("info", "tls", "seeded root CA %q to /etc/ssl", ca.Name)
	}
	// the household and the infra servers themselves trust the household root
	for _, id := range []string{"pc-alex", "router-alex", "nas-alex", "mirror", "prov-api", "bank", "jobs"} {
		d := w.Devices[id]
		if d == nil {
			continue
		}
		d.FS.MkdirAll("/etc/ssl/certs", 0755, "root", "root")
		d.FS.Write("/etc/ssl/certs/neohome-root-ca.pem", PEMCert(ca), 0644, "root", "root")
	}
	for _, h := range httpsHosts {
		leaf, err := w.IssueCert(h.host, ca)
		if err != nil {
			continue // cannot happen on a fresh world; a host without TLS stays plaintext
		}
		d := w.Devices[h.devID]
		if d == nil {
			continue
		}
		d.FS.MkdirAll("/etc/ssl/certs", 0755, "root", "root")
		d.FS.MkdirAll("/etc/ssl/private", 0700, "root", "root")
		d.FS.Write("/etc/ssl/certs/"+h.host+".pem", PEMCert(leaf), 0644, "root", "root")
		d.FS.Write("/etc/ssl/private/"+h.host+".key", PEMKey(leaf), 0600, "root", "root")
		// one unit, two sockets: a web server with a certificate really
		// binds the plaintext port and 443 from the same service
		if svc := d.Services[h.svc]; svc != nil {
			svc.TLSCert = leaf.Name
		}
	}
}
