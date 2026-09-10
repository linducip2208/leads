// Package mail sends campaign email via SMTP with template variables and
// compliance headers. Providers other than custom SMTP plug in as Senders.
package mail

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Account carries decrypted SMTP credentials.
type Account struct {
	Host       string
	Port       int
	Encryption string // none | starttls | ssl
	Username   string
	Password   string
	FromName   string
	FromEmail  string
	ReplyTo    string
}

// Message is one outbound email.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
	Unsub   string // List-Unsubscribe URL (optional but recommended)
	MsgID   string
}

// Render substitutes {{variables}} in subject/body. Unknown keys stay as-is.
func Render(tpl string, vars map[string]string) string {
	out := tpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
		out = strings.ReplaceAll(out, "{{ "+k+" }}", v)
	}
	return out
}

// ContactVars builds the standard variable map.
func ContactVars(firstName, fullName, company, email string, extra map[string]string) map[string]string {
	name := strings.TrimSpace(firstName)
	if name == "" {
		name = strings.TrimSpace(fullName)
	}
	vars := map[string]string{
		"first_name": name,
		"name":       strings.TrimSpace(fullName),
		"company":    strings.TrimSpace(company),
		"email":      strings.TrimSpace(email),
	}
	for k, v := range extra {
		vars[k] = v
	}
	return vars
}

// Sender transmits messages.
type Sender interface {
	Send(a Account, m Message) error
}

// SMTPSender uses net/smtp with STARTTLS/SSL/plain.
type SMTPSender struct {
	Timeout time.Duration
}

func (s SMTPSender) timeout() time.Duration {
	if s.Timeout <= 0 {
		return 20 * time.Second
	}
	return s.Timeout
}

// Send delivers one message.
func (s SMTPSender) Send(a Account, m Message) error {
	addr := net.JoinHostPort(a.Host, itoa(a.Port))
	from := a.FromEmail
	headers := map[string]string{
		"From":         fmtAddr(a.FromName, a.FromEmail),
		"To":           m.To,
		"Subject":      m.Subject,
		"MIME-Version": "1.0",
		"Content-Type": `text/plain; charset="utf-8"`,
		"Message-ID":   m.MsgID,
	}
	if strings.TrimSpace(a.ReplyTo) != "" {
		headers["Reply-To"] = a.ReplyTo
	}
	if m.Unsub != "" {
		headers["List-Unsubscribe"] = "<" + m.Unsub + ">"
		headers["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
	}
	var b strings.Builder
	for k, v := range headers {
		if v == "" {
			continue
		}
		b.WriteString(k + ": " + v + "\r\n")
	}
	b.WriteString("\r\n" + m.Text + "\r\n")

	dial := func() (net.Conn, error) {
		return net.DialTimeout("tcp", addr, s.timeout())
	}
	if a.Encryption == "ssl" {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: s.timeout()},
			"tcp", addr, &tls.Config{ServerName: a.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		return smtpSend(conn, a, from, []string{m.To}, []byte(b.String()))
	}
	conn, err := dial()
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, a.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if a.Encryption == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: a.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		}
	}
	return smtpSendClient(c, a, from, []string{m.To}, []byte(b.String()))
}

func smtpSend(conn net.Conn, a Account, from string, to []string, msg []byte) error {
	c, err := smtp.NewClient(conn, a.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	return smtpSendClient(c, a, from, to, msg)
}

func smtpSendClient(c *smtp.Client, a Account, from string, to []string, msg []byte) error {
	if a.Username != "" {
		auth := smtp.PlainAuth("", a.Username, a.Password, a.Host)
		if ok, _ := c.Extension("AUTH"); !ok {
			// server offers no AUTH; proceed only for credential-less dev relays
			if a.Password != "" {
				return fmt.Errorf("mail: server does not advertise AUTH")
			}
		} else if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

// Check verifies connectivity + credentials without sending anything.
func (s SMTPSender) Check(a Account) error {
	addr := net.JoinHostPort(a.Host, itoa(a.Port))
	var c *smtp.Client
	if a.Encryption == "ssl" {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: s.timeout()},
			"tcp", addr, &tls.Config{ServerName: a.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		c, err = smtp.NewClient(conn, a.Host)
		if err != nil {
			return err
		}
	} else {
		conn, err := net.DialTimeout("tcp", addr, s.timeout())
		if err != nil {
			return err
		}
		var err2 error
		c, err2 = smtp.NewClient(conn, a.Host)
		if err2 != nil {
			return err2
		}
		if a.Encryption == "starttls" {
			if ok, _ := c.Extension("STARTTLS"); ok {
				if err := c.StartTLS(&tls.Config{ServerName: a.Host, MinVersion: tls.VersionTLS12}); err != nil {
					c.Close()
					return err
				}
			}
		}
	}
	defer c.Close()
	if err := c.Hello("localhost"); err != nil {
		return err
	}
	if a.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return fmt.Errorf("mail: server does not advertise AUTH")
		}
		return c.Auth(smtp.PlainAuth("", a.Username, a.Password, a.Host))
	}
	return nil
}

func fmtAddr(name, email string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return email
	}
	return fmt.Sprintf("%s <%s>", name, email)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
