package mail

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// stubSMTP is a minimal in-memory SMTP server for tests.
type stubSMTP struct {
	t    *testing.T
	got  strings.Builder
	auth bool
}

func (s *stubSMTP) serve(ln net.Listener) {
	defer ln.Close()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	say := func(f string, a ...any) {
		s.t.Helper()
		msg := f
		if len(a) > 0 {
			msg = sprintf(f, a...)
		}
		if _, err := rw.WriteString(msg + "\r\n"); err != nil {
			return
		}
		rw.Flush()
	}
	say("220 stub ready")
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.Fields(line)[0])
		switch cmd {
		case "EHLO", "HELO":
			say("250-stub")
			say("250 AUTH PLAIN")
		case "AUTH":
			s.auth = true
			say("235 ok")
		case "MAIL", "RCPT":
			say("250 ok")
		case "DATA":
			say("354 end with .")
			var data strings.Builder
			for {
				l, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(l)
			}
			s.got.WriteString(data.String())
			say("250 queued")
		case "QUIT":
			say("221 bye")
			return
		case "RSET":
			say("250 ok")
		default:
			say("502 unimplemented")
		}
	}
}

func sprintf(f string, a ...any) string {
	// tiny Sprintf without importing fmt in test helper path
	out := f
	for _, v := range a {
		out = strings.Replace(out, "%s", v.(string), 1)
	}
	return out
}

func startStub(t *testing.T) (addr string, stub *stubSMTP) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stub = &stubSMTP{t: t}
	go stub.serve(ln)
	return ln.Addr().String(), stub
}

func splitAddr(addr string) (string, int) {
	host, port, _ := net.SplitHostPort(addr)
	var p int
	for _, r := range port {
		p = p*10 + int(r-'0')
	}
	return host, p
}

func TestRender(t *testing.T) {
	vars := ContactVars("Budi", "Budi Santoso", "PT Maju", "budi@maju.co.id", map[string]string{"unsubscribe_url": "http://x/u/1"})
	out := Render("Hi {{first_name}} of {{company}} — {{unsubscribe_url}}", vars)
	if out != "Hi Budi of PT Maju — http://x/u/1" {
		t.Fatalf("got %q", out)
	}
	if got := Render("Hello {{unknown}}", vars); got != "Hello {{unknown}}" {
		t.Fatalf("unknown vars must stay, got %q", got)
	}
}

func TestSendStub(t *testing.T) {
	addr, stub := startStub(t)
	host, port := splitAddr(addr)
	acc := Account{Host: host, Port: port, Encryption: "none", FromName: "Sales", FromEmail: "sales@leadforge.test"}
	msg := Message{To: "lead@x.co.id", Subject: "Hi", Text: "Body here", Unsub: "http://x/u/1", MsgID: "<1@x>"}
	if err := (SMTPSender{}).Send(acc, msg); err != nil {
		t.Fatal(err)
	}
	got := stub.got.String()
	for _, want := range []string{"Subject: Hi", "List-Unsubscribe: <http://x/u/1>", "Body here", "Message-ID: <1@x>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCheckStub(t *testing.T) {
	addr, _ := startStub(t)
	host, port := splitAddr(addr)
	acc := Account{Host: host, Port: port, Encryption: "none", FromEmail: "a@b.c"}
	if err := (SMTPSender{}).Check(acc); err != nil {
		t.Fatal(err)
	}
	bad := Account{Host: "127.0.0.1", Port: 1, Encryption: "none"}
	if err := (SMTPSender{}).Check(bad); err == nil {
		t.Fatal("expected connection error")
	}
}
