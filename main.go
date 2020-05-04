package main // import "github.com/carlmjohnson/certinfo"
import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"text/template"
	"time"

	"github.com/carlmjohnson/errutil"
	"github.com/carlmjohnson/exitcode"
	"github.com/carlmjohnson/flagext"
)

func main() {
	exitcode.Exit(exec(os.Args))
}

const usage = `Usage of certinfo

    certinfo [options] <host>...

Options:
`

func exec(args []string) error {
	fl := flag.NewFlagSet("json-tidy", flag.ContinueOnError)
	port := fl.Int("port", 443, "Port to look for TLS certificates on")
	verbose := fl.Bool("verbose", false, "log connections")
	timeout := fl.Duration("timeout", 5*time.Second, "time out on TCP dialing")
	expires := fl.Duration("expires", 7*24*time.Hour,
		"error if cert expiration time is less than this; use 0 to disable")
	mode := fl.String("output", "text", "json, text or none") //"json"
	//mode := "text"
	fl.Var(
		flagext.Choice(*mode, "json", "text", "none"),
		"output",
		"output `mode`: text, json, or none")
	fl.Usage = func() {
		fmt.Fprintf(fl.Output(), usage)
		fl.PrintDefaults()
	}

	if err := fl.Parse(args); err != nil {
		return flag.ErrHelp
	}
	if !*verbose {
		log.SetOutput(ioutil.Discard)
	}
	hosts := hostsFrom(fl.Args())

	returnInfo := make([]hostinfo, 0, len(hosts))
	var errs errutil.Slice
	for _, host := range hosts {
		info := hostinfo{Host: host, Port: *port}
		err := info.getCerts(*timeout)
		errs.Push(err)
		if err == nil {
			returnInfo = append(returnInfo, info)
		}
	}

	switch mode {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		err := enc.Encode(&returnInfo)
		errs.Push(err)

	case "text":
		t := template.Must(template.New("").Parse(`
{{- range . -}}
Host: {{ .Host }}:{{ .Port }}
Certs:
    {{ range .Certs -}}
    Issuer: {{ .Issuer.CommonName }}
    Subject: {{ .Subject.CommonName }}
    Not Before: {{ .NotBefore.Format "Jan 2, 2006 3:04 PM" }}
    Not After: {{ .NotAfter.Format "Jan 2, 2006 3:04 PM" }}
    DNS names: {{ range .DNSNames }}{{ . }} {{ end }}
{{ end }}
{{ end -}}
        `))
		err := t.Execute(os.Stdout, &returnInfo)
		errs.Push(err)

	case "none":
	}

	if *expires != 0 {
		deadline := time.Now().Add(*expires)
		for _, hi := range returnInfo {
			for _, c := range hi.Certs {
				if deadline.After(c.NotAfter) {
					err := fmt.Errorf("cert for %s expires too soon: %s less than %s away",
						c.Subject.CommonName,
						c.NotAfter.Format(time.RFC3339),
						expires)
					errs.Push(err)
				}
			}
		}
	}

	return errs.Merge()
}

func hostsFrom(ss []string) []string {
	for i, s := range ss {
		u, _ := url.Parse(s)
		if host := u.Hostname(); host != "" {
			ss[i] = host
		}
	}
	return ss
}

type hostinfo struct {
	Host  string
	Port  int
	Certs []*x509.Certificate
}

func (h *hostinfo) getCerts(timeout time.Duration) error {
	log.Printf("connecting to %s:%d", h.Host, h.Port)
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(
		dialer,
		"tcp",
		h.Host+":"+strconv.Itoa(h.Port),
		&tls.Config{
			InsecureSkipVerify: true,
		})
	if err != nil {
		return err
	}

	defer conn.Close()

	if err := conn.Handshake(); err != nil {
		return err
	}

	pc := conn.ConnectionState().PeerCertificates
	h.Certs = make([]*x509.Certificate, 0, len(pc))
	for _, cert := range pc {
		if cert.IsCA {
			continue
		}
		h.Certs = append(h.Certs, cert)
	}

	return nil
}
