package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestParseAddresses(t *testing.T) {
	got, err := ParseAddresses("10.0.0.1, server.example:2222\n10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.1:22", "server.example:2222"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("addresses = %#v, want %#v", got, want)
	}
}

func TestParseAddressesRejectsInvalidPort(t *testing.T) {
	for _, address := range []string{"server:", "server:ssh", "server:70000"} {
		if _, err := ParseAddresses(address); err == nil {
			t.Fatalf("expected invalid address error for %q", address)
		}
	}
}

func TestParseAddressesSupportsIPv6(t *testing.T) {
	got, err := ParseAddresses("2001:db8::1, [2001:db8::2]:2222")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"[2001:db8::1]:22", "[2001:db8::2]:2222"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("addresses = %#v, want %#v", got, want)
	}
}

func TestLoadScript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadScript(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "#!/bin/sh\necho ok\n" {
		t.Fatalf("unexpected script: %q", got)
	}
}

func TestExecuteValidatesWithoutConnecting(t *testing.T) {
	results := Execute(context.Background(), Request{Servers: []Server{{Address: "localhost:22"}}})
	if len(results) != 1 || results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "user") {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestExecuteRunsScriptOverSSH(t *testing.T) {
	address, received, stop := startTestSSHServer(t, "secret")
	defer stop()
	request := Request{
		Servers:    []Server{{Address: address, User: "tester", Password: "secret"}},
		Script:     []byte("echo integration-test\n"),
		ScriptArgs: []string{"bond2.306", "ip=10.0.36.87", "description=operator's vlan"},
		Timeout:    3 * time.Second,
		HostKeys:   ssh.InsecureIgnoreHostKey(), // isolated ephemeral test server
	}
	results := Execute(context.Background(), request)
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("unexpected SSH results: %#v", results)
	}
	if results[0].Output != "server-output\n" {
		t.Fatalf("output = %q", results[0].Output)
	}
	got := <-received
	wantCommand := `bash -s -- 'bond2.306' 'ip=10.0.36.87' 'description=operator'"'"'s vlan'`
	if got.command != wantCommand || got.stdin != "echo integration-test\n" {
		t.Fatalf("remote request = %#v", got)
	}
}

func TestTOFUHostKeyCallbackRejectsChangedKey(t *testing.T) {
	callback, err := NewTOFUHostKeyCallback(filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	first := newTestSigner(t).PublicKey()
	changed := newTestSigner(t).PublicKey()
	remoteAddress := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}
	if err := callback("server.example:22", remoteAddress, first); err != nil {
		t.Fatalf("first use was not trusted: %v", err)
	}
	if err := callback("server.example:22", remoteAddress, first); err != nil {
		t.Fatalf("stored key was not trusted: %v", err)
	}
	if err := callback("server.example:22", remoteAddress, changed); err == nil {
		t.Fatal("changed host key was trusted")
	}
}

type testSSHRequest struct {
	command string
	stdin   string
}

func startTestSSHServer(t *testing.T, password string) (string, <-chan testSSHRequest, func()) {
	t.Helper()
	signer := newTestSigner(t)
	config := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, supplied []byte) (*ssh.Permissions, error) {
		if string(supplied) != password {
			return nil, fmt.Errorf("incorrect password")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan testSSHRequest, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_, channels, requests, err := ssh.NewServerConn(connection, config)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(requests)
		for channelRequest := range channels {
			if channelRequest.ChannelType() != "session" {
				_ = channelRequest.Reject(ssh.UnknownChannelType, "session required")
				continue
			}
			channel, sessionRequests, err := channelRequest.Accept()
			if err != nil {
				return
			}
			for request := range sessionRequests {
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				if ssh.Unmarshal(request.Payload, &payload) != nil {
					_ = request.Reply(false, nil)
					continue
				}
				_ = request.Reply(true, nil)
				stdin, _ := io.ReadAll(channel)
				received <- testSSHRequest{command: payload.Command, stdin: string(stdin)}
				_, _ = channel.Write([]byte("server-output\n"))
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				_ = channel.Close()
				return
			}
		}
	}()
	stop := func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
	return listener.Addr().String(), received, stop
}

func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
