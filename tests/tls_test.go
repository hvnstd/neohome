package tests

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
)

// A fresh world must already carry a root CA as real state, with its
// material on the assistant node's disk — not a stub.
func TestTLSSeedsRootCAWithFiles(t *testing.T) {
	w := core.NewWorld()
	s := w.TLS
	if s == nil || len(s.CAs) == 0 {
		t.Fatal("a fresh world has no certificate authority")
	}
	ca, ok := s.CAs["neohome-root-ca"]
	if !ok {
		t.Fatalf("seeded CA missing, got %v", s.CAs)
	}
	if !ca.IsCA || ca.Issuer != "" {
		t.Fatal("the seeded root is not a self-signed root")
	}

	asst := w.Devices["asst-alex"]
	if asst == nil {
		t.Fatal("assistant node missing from the world")
	}
	pemData, ok := asst.FS.Read("/etc/ssl/certs/neohome-root-ca.pem")
	if !ok || !strings.Contains(string(pemData), "BEGIN CERTIFICATE") {
		t.Fatal("CA certificate is not a readable PEM file on the assistant node")
	}
	// the file on disk must be a view of the world's CA, byte for byte
	if string(pemData) != core.PEMCert(ca) {
		t.Fatal("the PEM on disk does not match the world's CA certificate")
	}
	if _, ok := asst.FS.Read("/etc/ssl/private/neohome-root-ca.key"); !ok {
		t.Fatal("CA private key missing from /etc/ssl/private")
	}
	// the key directory is private: a non-root account cannot read the key
	if u := asst.FindUser("assistant"); u != nil {
		if asst.FS.CanRead("/etc/ssl/private/neohome-root-ca.key", u) {
			t.Fatal("the CA private key is readable by a non-root user")
		}
	}
}

func TestTLSGenerateCAAndDuplicateRefused(t *testing.T) {
	w := core.NewWorld()
	ca2, err := w.GenerateCA("lab-ca")
	if err != nil {
		t.Fatalf("creating a second CA failed: %v", err)
	}
	if _, err := w.GenerateCA("lab-ca"); err == nil {
		t.Fatal("issuing a duplicate CA name must be refused")
	}
	if _, err := w.GenerateCA(""); err == nil {
		t.Fatal("an unnamed CA must be refused")
	}
	if w.TLS.CAs["lab-ca"] != ca2 {
		t.Fatal("the new CA is not registered under its name")
	}
}

func TestTLSIssueAndVerify(t *testing.T) {
	w := core.NewWorld()
	ca := w.TLS.CAs["neohome-root-ca"]
	leaf, err := w.IssueCert("example.com", ca)
	if err != nil {
		t.Fatalf("issuing a leaf failed: %v", err)
	}
	if leaf.Name != "example.com" || leaf.Issuer != ca.Name {
		t.Fatalf("leaf metadata wrong: name=%q issuer=%q", leaf.Name, leaf.Issuer)
	}
	// the leaf really carries the domain as CN and DNS SAN
	crt, err := core.ParseCert(leaf)
	if err != nil {
		t.Fatalf("issued DER does not parse: %v", err)
	}
	if crt.Subject.CommonName != "example.com" {
		t.Fatalf("bad CN: %q", crt.Subject.CommonName)
	}
	if len(crt.DNSNames) != 1 || crt.DNSNames[0] != "example.com" {
		t.Fatalf("bad DNS SANs: %v", crt.DNSNames)
	}
	if !w.Verify(leaf, ca) {
		t.Fatal("a certificate signed by the world's own CA does not verify")
	}
	// verify fails against a CA that is not the issuer
	other, _ := w.GenerateCA("other-ca")
	if w.Verify(leaf, other) {
		t.Fatal("a leaf verified against a CA that did not sign it")
	}
	// and against a CA that is not part of this world at all
	if w.Verify(leaf, &core.TLSCert{Name: "ghost", IsCA: true}) {
		t.Fatal("a leaf verified against a CA unknown to the world")
	}
	// issuing for an unknown/foreign CA object is refused
	if _, err := w.IssueCert("x.example", other); err != nil {
		// other IS registered, so this should have succeeded; only a foreign
		// object must be refused
		t.Fatalf("issuing by a registered CA failed: %v", err)
	}
	if _, err := w.IssueCert("y.example", &core.TLSCert{Name: "ghost", IsCA: true}); err == nil {
		t.Fatal("issuing by a CA that is not part of this world must be refused")
	}
	if _, err := w.IssueCert("", ca); err == nil {
		t.Fatal("issuing without a domain must be refused")
	}
}

func TestTLSClockGatesIssuanceAndVerification(t *testing.T) {
	w := core.NewWorld()
	ca := w.TLS.CAs["neohome-root-ca"]
	leaf, err := w.IssueCert("seasonal.example", ca)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	// a leaf lives one simulated year: past that, real clients stop trusting it
	w.Sim = leaf.NotAfter.Add(24 * time.Hour)
	if w.Verify(leaf, ca) {
		t.Fatal("an expired certificate still verifies")
	}
	// past the CA's own validity, nothing new may be issued by it
	w.Sim = ca.NotAfter.Add(24 * time.Hour)
	if _, err := w.IssueCert("late.example", ca); err == nil {
		t.Fatal("issuing from an expired CA must be refused")
	}
	// and the world did not record a certificate it refused to issue
	for _, c := range w.TLS.Issued {
		if c.Name == "late.example" {
			t.Fatal("the refused certificate was recorded anyway")
		}
	}
}

func TestTLSPersistenceRoundTrip(t *testing.T) {
	w := core.NewWorld()
	ca := w.TLS.CAs["neohome-root-ca"]
	leaf, err := w.IssueCert("keeper.example", ca)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	other, _ := w.GenerateCA("vault-ca")
	if _, err := w.IssueCert("vault.example", other); err != nil {
		t.Fatalf("issue failed: %v", err)
	}

	path := filepath.Join(t.TempDir(), "tls_world.gob")
	if err := w.Save(path); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	w2, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	// saves from a version without TLS load as an empty-but-honest state
	if w2.TLS == nil {
		t.Fatal("loaded world lost its TLS state")
	}
	if len(w2.TLS.CAs) != 2 {
		t.Fatalf("CAs lost in transit: %d != 2", len(w2.TLS.CAs))
	}
	ca2 := w2.TLS.CAs["neohome-root-ca"]
	if ca2 == nil || !bytes.Equal(ca2.CertDER, ca.CertDER) {
		t.Fatal("the CA certificate bytes did not survive the round trip")
	}
	found := false
	for _, c := range w2.TLS.Issued {
		if c.Name == "keeper.example" {
			found = true
			if !bytes.Equal(c.CertDER, leaf.CertDER) {
				t.Fatal("the leaf bytes changed across save/load")
			}
			if !w2.Verify(c, ca2) {
				t.Fatal("a reloaded certificate no longer verifies")
			}
		}
	}
	if !found {
		t.Fatal("issued certificates were lost in transit")
	}
}

// repairDNS applies the same repair a player performs for the scripted
// router fault (see cron_test.go): point dnsmasq at a working upstream and
// restart it, so the tests below exercise https rather than the DNS story.
func repairDNS(t *testing.T, w *core.World) {
	t.Helper()
	router := w.Devices["router-alex"]
	if router == nil {
		t.Fatal("no router in the world")
	}
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")
	data, _ := router.FS.Read("/etc/dnsmasq.conf")
	router.FS.Write("/etc/dnsmasq.conf",
		strings.Replace(string(data), "/var/run/dnsmasq/resolv.conf", "/etc/dnsmasq.upstream", 1),
		0644, "root", "root")
	if _, err := router.RestartService("dnsmasq"); err != nil {
		t.Fatalf("restart dnsmasq: %v", err)
	}
}

// The seeded world speaks real https: a client with the household root in
// its trust store gets the page, and every broken-link-in-the-chain case
// fails closed with the real client error.
func TestHTTPSFetchFailsClosed(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	out := sh("curl -s https://mirror.neohome.example/")
	if !strings.Contains(out, "mirror.neohome.example index") {
		t.Fatalf("https fetch against the seeded mirror did not serve the page:\n%s", out)
	}
	out = sh("curl -s https://bank.firstneohome.example/")
	if !strings.Contains(out, "bank API") {
		t.Fatalf("https fetch against the seeded bank did not serve the API:\n%s", out)
	}

	// the mirror really answers on 443 and scan sees it
	mirror := w.Devices["mirror"]
	if svc := mirror.Svc("nginx"); svc == nil || svc.TLSCert == "" {
		t.Fatal("the mirror's nginx unit does not carry TLS material")
	}

	// breaking the client trust store breaks https, with the real error
	if !pc.FS.Remove("/etc/ssl/certs/neohome-root-ca.pem") {
		t.Fatal("the seeded trust store file could not be removed")
	}
	out = sh("curl -s https://mirror.neohome.example/")
	if !strings.Contains(out, "(60)") || !strings.Contains(out, "unable to get local issuer certificate") {
		t.Fatalf("untrusted CA must fail closed with a certificate error:\n%s", out)
	}
	// ...and the failure is diagnosable: s_client shows the same verdict
	out = sh("openssl s_client -connect mirror.neohome.example:443")
	if !strings.Contains(out, "CONNECTED") || !strings.Contains(out, "unable to get local issuer certificate") {
		t.Fatalf("s_client does not report the untrusted CA:\n%s", out)
	}
	// restoring trust restores the page — the file is the trust
	pc.FS.Write("/etc/ssl/certs/neohome-root-ca.pem", core.PEMCert(w.TLS.CAs["neohome-root-ca"]), 0644, "root", "root")
	out = sh("curl -s https://mirror.neohome.example/")
	if !strings.Contains(out, "mirror.neohome.example index") {
		t.Fatalf("https did not recover after restoring the trust store:\n%s", out)
	}
}

// Service state and the clock gate https the way they gate everything else.
func TestHTTPSRespectsServiceStateAndClock(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	// a stopped web server refuses the connection on 443 like on 80
	if _, err := w.Devices["mirror"].StopService("nginx"); err != nil {
		t.Fatalf("stopping nginx failed: %v", err)
	}
	out := sh("curl -s https://mirror.neohome.example/")
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("https must refuse when the unit is stopped:\n%s", out)
	}

	// back up, the page works; then the certificate expires and it must not
	if _, err := w.Devices["mirror"].StartService("nginx"); err != nil {
		t.Fatalf("starting nginx failed: %v", err)
	}
	var leaf *core.TLSCert
	for _, c := range w.TLS.Issued {
		if c.Name == "mirror.neohome.example" {
			leaf = c
		}
	}
	if leaf == nil {
		t.Fatal("the mirror leaf certificate was not issued at seed time")
	}
	out = sh("curl -s https://mirror.neohome.example/")
	if !strings.Contains(out, "mirror.neohome.example index") {
		t.Fatalf("https broke after a plain service restart:\n%s", out)
	}
	w.Sim = leaf.NotAfter.Add(24 * time.Hour)
	out = sh("curl -s https://mirror.neohome.example/")
	if !strings.Contains(out, "certificate has expired") {
		t.Fatalf("an expired certificate must fail the handshake:\n%s", out)
	}
}

// The handshake judges the name on the certificate, not just the chain.
func TestTLSHandshakeChecksHostnameAndPresence(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	ca := w.TLS.CAs["neohome-root-ca"]

	// a service without TLS material presents no certificate
	noTLS := &core.Service{Name: "plain", Port: 443, Proto: "tcp", Scope: "any", State: "running"}
	if _, _, err := w.Handshake(pc, "whatever.example", noTLS); err == nil {
		t.Fatal("a service without a certificate must not complete a handshake")
	}

	// the bank's certificate does not answer for the mirror's name
	var bankLeaf *core.TLSCert
	for _, c := range w.TLS.Issued {
		if c.Name == "bank.firstneohome.example" {
			bankLeaf = c
		}
	}
	if bankLeaf == nil {
		t.Fatal("the bank leaf certificate was not issued at seed time")
	}
	wrong := &core.Service{Name: "nginx", Port: 443, Proto: "tcp", Scope: "any", State: "running", TLSCert: bankLeaf.Name}
	if _, _, err := w.Handshake(pc, "mirror.neohome.example", wrong); err == nil {
		t.Fatal("a certificate for another host must not verify for this name")
	}

	// the right name with the right chain verifies
	right := &core.Service{Name: "nginx", Port: 443, Proto: "tcp", Scope: "any", State: "running", TLSCert: "mirror.neohome.example"}
	if l, issuer, err := w.Handshake(pc, "mirror.neohome.example", right); err != nil || l == nil || issuer != ca {
		t.Fatalf("the honest handshake must succeed: leaf=%v err=%v", l, err)
	}
}

// openssl reads the material that is really on disk.
func TestOpensslReadsRealFiles(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	out := sh("openssl x509 -in /etc/ssl/certs/neohome-root-ca.pem -noout -subject -dates")
	if !strings.Contains(out, "CN = neohome-root-ca") {
		t.Fatalf("openssl x509 does not show the seeded CA:\n%s", out)
	}
	if !strings.Contains(out, "notAfter=") {
		t.Fatalf("openssl x509 does not show validity dates:\n%s", out)
	}
	out = sh("openssl s_client -connect mirror.neohome.example:443")
	if !strings.Contains(out, "subject=CN = mirror.neohome.example") ||
		!strings.Contains(out, "issuer=CN = neohome-root-ca") ||
		!strings.Contains(out, "Verify return code: 0 (ok)") {
		t.Fatalf("s_client does not report the real chain:\n%s", out)
	}
}
