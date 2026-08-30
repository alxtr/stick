package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	netsmtp "net/smtp"
	"time"
)

// sendMailContext is the context-aware equivalent of smtp.SendMail. The
// standard helper has no context or deadline support, so the connection is
// dialed with DialContext and closed when the context is canceled.
func sendMailContext(ctx context.Context, addr, tlsMode string, auth netsmtp.Auth, from string, to []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}

	var conn net.Conn
	if tlsMode == TLSModeImplicit {
		dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: tlsConfig}
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	} else {
		dialer := net.Dialer{}
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return contextError(ctx, err)
	}

	stopClosing := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopClosing:
		}
	}()
	defer close(stopClosing)
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := netsmtp.NewClient(conn, host)
	if err != nil {
		return contextError(ctx, err)
	}
	defer client.Close()
	if err := client.Hello("localhost"); err != nil {
		return contextError(ctx, err)
	}
	if tlsMode == TLSModeStartTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return fmt.Errorf("SMTP server does not support required STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return contextError(ctx, err)
		}
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return contextError(ctx, err)
		}
	}
	if err := client.Mail(from); err != nil {
		return contextError(ctx, err)
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return contextError(ctx, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return contextError(ctx, err)
	}
	if _, err := writer.Write(msg); err != nil {
		return contextError(ctx, err)
	}
	if err := writer.Close(); err != nil {
		return contextError(ctx, err)
	}
	if err := client.Quit(); err != nil {
		return contextError(ctx, err)
	}
	return nil
}

func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return context.DeadlineExceeded
		}
	}
	return err
}
