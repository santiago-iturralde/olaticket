package email

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// Config se lee desde el entorno (.env).
type Config struct {
	Host string // SMTP_HOST (default smtp.gmail.com)
	Port string // SMTP_PORT (default 587, STARTTLS)
	User string // SMTP_USER
	Pass string // SMTP_PASS: con Gmail, un App Password (no la contraseña de la cuenta)
	From string // SMTP_FROM (default SMTP_USER)
}

func ConfigFromEnv() Config {
	user := strings.TrimSpace(os.Getenv("SMTP_USER"))
	return Config{
		Host: envOr("SMTP_HOST", "smtp.gmail.com"),
		Port: envOr("SMTP_PORT", "587"),
		User: user,
		// Gmail muestra el App Password en bloques separados por espacios.
		Pass: strings.ReplaceAll(strings.TrimSpace(os.Getenv("SMTP_PASS")), " ", ""),
		From: envOr("SMTP_FROM", user),
	}
}

func (c Config) Enabled() bool {
	return c.User != "" && c.Pass != ""
}

type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

type Message struct {
	To          string
	Subject     string
	HTML        string
	Attachments []Attachment
}

type Sender struct {
	cfg Config
}

func NewSender(cfg Config) *Sender {
	return &Sender{cfg: cfg}
}

func (s *Sender) Enabled() bool {
	return s.cfg.Enabled()
}

// Send envía el mensaje por SMTP. smtp.SendMail negocia STARTTLS si el servidor lo ofrece
// y se niega a autenticar en texto plano fuera de localhost.
func (s *Sender) Send(msg Message) error {
	if !s.Enabled() {
		return fmt.Errorf("SMTP no configurado (faltan SMTP_USER / SMTP_PASS)")
	}
	if strings.ContainsAny(msg.To, "\r\n") || strings.ContainsAny(msg.Subject, "\r\n") {
		return fmt.Errorf("destinatario o asunto inválido")
	}
	raw, err := s.build(msg)
	if err != nil {
		return err
	}
	auth := smtp.PlainAuth("", s.cfg.User, s.cfg.Pass, s.cfg.Host)
	addr := net.JoinHostPort(s.cfg.Host, s.cfg.Port)
	if err := smtp.SendMail(addr, auth, s.cfg.User, []string{msg.To}, raw); err != nil {
		return fmt.Errorf("error enviando email a %s: %w", msg.To, err)
	}
	return nil
}

func (s *Sender) build(msg Message) ([]byte, error) {
	boundary := fmt.Sprintf("olaticket-%d", time.Now().UnixNano())
	var b bytes.Buffer

	fmt.Fprintf(&b, "From: Olaticket <%s>\r\n", s.cfg.From)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64(&b, []byte(msg.HTML))

	for _, a := range msg.Attachments {
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: %s; name=%q\r\n", a.ContentType, a.Filename)
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&b, "Content-Disposition: attachment; filename=%q\r\n\r\n", a.Filename)
		writeBase64(&b, a.Data)
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes(), nil
}

// writeBase64 escribe en líneas de 76 caracteres (límite de RFC 2045).
func writeBase64(b *bytes.Buffer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		b.WriteString(encoded[:76])
		b.WriteString("\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded)
	b.WriteString("\r\n")
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
