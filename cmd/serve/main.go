// Command serve serves the static page over HTTPS with a self-signed certificate.
// The File System Access API needs a secure context.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8000", "listen address")
	dir := flag.String("dir", "dist", "page directory (the output of build.sh)")
	flag.Parse()
	cert, err := selfSigned()
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/", http.FileServer(http.Dir(*dir)))
	server := &http.Server{Addr: *addr, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
	log.Printf("serving %s on https://%s/", *dir, *addr)
	log.Fatal(server.ListenAndServeTLS("", ""))
}

// selfSigned makes a certificate for this run that names the host and its addresses.
func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("get hostname: %w", err)
	}
	local, err := net.InterfaceAddrs()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("list addresses: %w", err)
	}
	template := x509.Certificate{
		SerialNumber: serial,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, 30),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", host},
	}
	for _, a := range local {
		if ip, ok := a.(*net.IPNet); ok {
			template.IPAddresses = append(template.IPAddresses, ip.IP)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
