package shell

// openssl: read-only diagnosis of the world's real certificate state. The
// material it shows lives in the device filesystem (PEM views) and in
// World.TLS (the truth), and s_client performs the same handshake a real
// client would — including its failure modes — rather than printing a
// canned chain.

import (
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"time"

	"neohome/internal/core"
)

func init() {
	builtinTable["openssl"] = cmdOpenssl
}

func cmdOpenssl(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: openssl s_client -connect HOST[:PORT] | openssl x509 -in FILE [-noout] [-subject|-issuer|-dates|-text]")
		return 1
	}
	switch args[0] {
	case "s_client":
		return opensslSClient(s, args[1:])
	case "x509":
		return opensslX509(s, args[1:])
	}
	s.errf("openssl: unknown subcommand %q (try s_client or x509)", args[0])
	return 1
}

// opensslSClient dials HOST[:PORT] (default 443), performs the real
// handshake and prints what a real client sees: the presented certificate,
// its issuer and validity, and the verify result.
func opensslSClient(s *Shell, args []string) int {
	target := ""
	for i, a := range args {
		if a == "-connect" && i+1 < len(args) {
			target = args[i+1]
		}
	}
	if target == "" {
		s.errf("usage: openssl s_client -connect HOST[:PORT]")
		return 1
	}
	host := target
	port := 443
	if i := strings.Index(target, ":"); i >= 0 {
		host = target[:i]
		if _, err := fmt.Sscanf(target[i+1:], "%d", &port); err != nil || port <= 0 {
			s.errf("openssl: bad port in %q", target)
			return 1
		}
	}
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("getaddrinfo: %s (%s)", host, how)
		return 1
	}
	svc, _, msg := core.Dial(s.Dev, ip, port)
	fmt.Fprintf(s.Out, "CONNECTED(00000003)\n")
	if svc == nil {
		s.errf("connect: %s", msg)
		return 1
	}
	leaf, ca, err := s.W.Handshake(s.Dev, host, svc)
	if err != nil {
		var tlsErr *core.TLSError
		if !errors.As(err, &tlsErr) {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "verify error:num=%d:%s\n", tlsErr.Code, tlsErr.Msg)
		fmt.Fprintf(s.Out, "Verify return code: %d (%s)\n", tlsErr.Code, tlsErr.Msg)
		return 1
	}
	crt, err := core.ParseCert(leaf)
	if err != nil {
		s.errf("openssl: presented certificate does not parse: %v", err)
		return 1
	}
	caCrt, _ := core.ParseCert(ca)
	fmt.Fprintf(s.Out, "depth=0 CN = %s\n", crt.Subject.CommonName)
	fmt.Fprintf(s.Out, "verify return:1\n---\n")
	fmt.Fprintf(s.Out, "Certificate chain\n")
	fmt.Fprintf(s.Out, " 0 s:CN = %s, O = %s\n", crt.Subject.CommonName, orgName(crt))
	fmt.Fprintf(s.Out, "   i:CN = %s, O = %s\n", caCrt.Subject.CommonName, orgName(caCrt))
	fmt.Fprintf(s.Out, "---\nServer certificate\n")
	fmt.Fprintf(s.Out, "subject=CN = %s, O = %s\n", crt.Subject.CommonName, orgName(crt))
	fmt.Fprintf(s.Out, "issuer=CN = %s, O = %s\n", caCrt.Subject.CommonName, orgName(caCrt))
	fmt.Fprintf(s.Out, "notBefore=%s\n", gmtTime(crt.NotBefore))
	fmt.Fprintf(s.Out, "notAfter=%s\n", gmtTime(crt.NotAfter))
	fmt.Fprintf(s.Out, "---\nVerify return code: 0 (ok)\n")
	return 0
}

// opensslX509 reads a PEM certificate from the device filesystem and shows
// the requested fields; without -noout the PEM itself is printed, as the
// real tool does.
func opensslX509(s *Shell, args []string) int {
	file := ""
	noout := false
	want := map[string]bool{}
	for i, a := range args {
		switch {
		case a == "-in" && i+1 < len(args):
			file = args[i+1]
		case a == "-noout":
			noout = true
		case a == "-subject" || a == "-issuer" || a == "-dates" || a == "-text":
			want[a] = true
		}
	}
	if file == "" {
		s.errf("usage: openssl x509 -in FILE [-noout] [-subject|-issuer|-dates|-text]")
		return 1
	}
	p := s.abs(file)
	vfs, p2, rerr := s.ResolveVFS(p)
	if vfs == nil {
		s.errf("openssl: %s: %s", file, rerr)
		return 1
	}
	data, exists, allowed := vfs.ReadPathAs(p2, s.User)
	if !exists {
		s.errf("openssl: %s: No such file or directory", file)
		return 1
	}
	if !allowed {
		s.errf("openssl: %s: Permission denied", file)
		return 1
	}
	crt, perr := core.ParsePEM(string(data))
	if perr != nil {
		s.errf("openssl: %s: no certificate in %s", file, file)
		return 1
	}
	if want["-subject"] {
		fmt.Fprintf(s.Out, "subject=CN = %s, O = %s\n", crt.Subject.CommonName, orgName(crt))
	}
	if want["-issuer"] {
		fmt.Fprintf(s.Out, "issuer=CN = %s, O = %s\n", crt.Issuer.CommonName, orgName(crt))
	}
	if want["-dates"] {
		fmt.Fprintf(s.Out, "notBefore=%s\n", gmtTime(crt.NotBefore))
		fmt.Fprintf(s.Out, "notAfter=%s\n", gmtTime(crt.NotAfter))
	}
	if want["-text"] {
		fmt.Fprintf(s.Out, "Certificate:\n")
		fmt.Fprintf(s.Out, "    Signature Algorithm: sha256WithRSAEncryption\n")
		fmt.Fprintf(s.Out, "        Subject: CN = %s\n", crt.Subject.CommonName)
		fmt.Fprintf(s.Out, "        DNS:%s\n", strings.Join(crt.DNSNames, ", DNS:"))
		fmt.Fprintf(s.Out, "    Public-Key: (2048 bit)\n")
	}
	if !noout {
		fmt.Fprint(s.Out, string(data))
	}
	return 0
}

func orgName(crt *x509.Certificate) string {
	if len(crt.Subject.Organization) > 0 {
		return crt.Subject.Organization[0]
	}
	return "-"
}

func gmtTime(t time.Time) string {
	return t.UTC().Format("Oct  2 15:04:05 2006 GMT")
}
