package smtp_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"stick/internal/adapters/notification/smtp"
	notify "stick/internal/notification"
)

func TestSMTPNotifierDeliversOverSTARTTLS(t *testing.T) {
	certificate, rootPEM := testSMTPServerCertificate(t)
	rootPath := filepath.Join(t.TempDir(), "smtp-root.pem")
	if err := os.WriteFile(rootPath, rootPEM, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", rootPath)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	message := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- serveTestSMTP(conn, certificate, message)
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	notifier, err := smtp.New(
		smtp.Config{Host: host, Port: port, Username: "user", Password: "pass", From: "noreply@example.com"},
		smtp.Templates{Subject: "{{.StickName}} is available", Body: "Hello {{.RecipientName}}"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.Notify(context.Background(), notify.Notification{
		StickName:      "prod-deploy",
		RecipientName:  "Alice",
		RecipientEmail: "alice@example.com",
	}); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("SMTP server: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP server did not finish")
	}
	select {
	case body := <-message:
		text := string(body)
		for _, want := range []string{"From: noreply@example.com", "To: alice@example.com", "Subject: prod-deploy is available", "Hello Alice"} {
			if !strings.Contains(text, want) {
				t.Errorf("message missing %q: %s", want, text)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP server did not capture a message")
	}
}

func serveTestSMTP(conn net.Conn, certificate tls.Certificate, message chan<- []byte) error {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	write := func(response string) error {
		_, err := io.WriteString(conn, response)
		return err
	}
	if err := write("220 localhost ESMTP\r\n"); err != nil {
		return err
	}
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	if err := write("250-localhost\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n"); err != nil {
		return err
	}
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	if err := write("220 Ready to start TLS\r\n"); err != nil {
		return err
	}
	tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}})
	if err := tlsConn.Handshake(); err != nil {
		return err
	}
	conn = tlsConn
	reader = bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	if err := write("250-localhost\r\n250 AUTH PLAIN\r\n"); err != nil {
		return err
	}
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	if err := write("235 2.7.0 authenticated\r\n"); err != nil {
		return err
	}
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	if err := write("250 2.1.0 sender ok\r\n"); err != nil {
		return err
	}
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	if err := write("250 2.1.5 recipient ok\r\n"); err != nil {
		return err
	}
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	if err := write("354 end with <CRLF>.<CRLF>\r\n"); err != nil {
		return err
	}
	var body bytes.Buffer
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == ".\r\n" {
			break
		}
		body.WriteString(line)
	}
	message <- body.Bytes()
	if err := write("250 2.0.0 queued\r\n"); err != nil {
		return err
	}
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	return write("221 2.0.0 bye\r\n")
}

func testSMTPServerCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Stick SMTP Test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{
		Certificate: [][]byte{serverDER},
		PrivateKey:  serverKey,
	}
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	return certificate, rootPEM
}
