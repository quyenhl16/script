package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const DefaultTimeout = 30 * time.Second

type Server struct {
	Address  string
	User     string
	Password string
}

type Request struct {
	Servers  []Server
	Command  string
	Script   []byte
	Timeout  time.Duration
	HostKeys ssh.HostKeyCallback
}

type Result struct {
	Address  string
	Output   string
	Duration time.Duration
	Err      error
}

// ParseAddresses accepts comma, semicolon, whitespace, or newline-separated
// host names. Port 22 is added when a port is not supplied.
func ParseAddresses(value string) ([]string, error) {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	addresses := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, part := range parts {
		address, err := normalizeAddress(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		if !seen[address] {
			seen[address] = true
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("enter at least one server address")
	}
	return addresses, nil
}

func normalizeAddress(value string) (string, error) {
	if value == "" {
		return "", errors.New("server address cannot be empty")
	}
	if host, port, err := net.SplitHostPort(value); err == nil {
		if host == "" || port == "" {
			return "", fmt.Errorf("invalid server address %q", value)
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", fmt.Errorf("invalid SSH port in %q", value)
		}
		return net.JoinHostPort(host, port), nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return net.JoinHostPort(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"), "22"), nil
	}
	if strings.Count(value, ":") > 1 { // unbracketed IPv6 without a port
		return net.JoinHostPort(value, "22"), nil
	}
	if strings.Contains(value, ":") {
		return "", fmt.Errorf("invalid server address %q; use host:port", value)
	}
	return net.JoinHostPort(value, "22"), nil
}

// LoadScript reads a local script with a conservative size limit. Its bytes are
// streamed to the remote shell and the path itself is never sent to the server.
func LoadScript(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("enter a local script path")
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open script: %w", err)
	}
	defer file.Close()
	const maxScriptSize = 10 << 20
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect script: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("script path must point to a regular file")
	}
	if info.Size() > maxScriptSize {
		return nil, errors.New("script is larger than 10 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxScriptSize+1))
	if err != nil {
		return nil, fmt.Errorf("read script: %w", err)
	}
	if len(data) > maxScriptSize {
		return nil, errors.New("script is larger than 10 MiB")
	}
	return data, nil
}

func Execute(ctx context.Context, request Request) []Result {
	results := make([]Result, len(request.Servers))
	var group sync.WaitGroup
	for index, server := range request.Servers {
		group.Add(1)
		go func() {
			defer group.Done()
			results[index] = executeOne(ctx, server, request)
		}()
	}
	group.Wait()
	return results
}

func executeOne(parent context.Context, server Server, request Request) (result Result) {
	started := time.Now()
	result = Result{Address: server.Address}
	defer func() { result.Duration = time.Since(started) }()

	if strings.TrimSpace(server.User) == "" {
		result.Err = errors.New("user cannot be empty")
		return
	}
	if server.Password == "" {
		result.Err = errors.New("password cannot be empty")
		return
	}
	if request.Command == "" && len(request.Script) == 0 {
		result.Err = errors.New("command or script is required")
		return
	}
	if request.Command != "" && len(request.Script) > 0 {
		result.Err = errors.New("choose either command or script")
		return
	}
	if request.HostKeys == nil {
		result.Err = errors.New("host key verifier is required")
		return
	}

	timeout := request.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", server.Address)
	if err != nil {
		result.Err = fmt.Errorf("connect: %w", err)
		return
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	connectionDone := make(chan struct{})
	defer close(connectionDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-connectionDone:
		}
	}()

	config := &ssh.ClientConfig{
		User:            server.User,
		Auth:            []ssh.AuthMethod{ssh.Password(server.Password)},
		HostKeyCallback: request.HostKeys,
		Timeout:         timeout,
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, server.Address, config)
	if err != nil {
		result.Err = fmt.Errorf("SSH handshake: %w", err)
		return
	}
	client := ssh.NewClient(clientConnection, channels, requests)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		result.Err = fmt.Errorf("create SSH session: %w", err)
		return
	}
	defer session.Close()
	if len(request.Script) > 0 {
		session.Stdin = bytes.NewReader(request.Script)
	}

	command := request.Command
	if len(request.Script) > 0 {
		command = "sh -s --"
	}
	done := make(chan struct{})
	var output []byte
	go func() {
		output, err = session.CombinedOutput(command)
		close(done)
	}()
	select {
	case <-ctx.Done():
		_ = client.Close()
		<-done
		result.Output = string(output)
		result.Err = fmt.Errorf("execution stopped: %w", ctx.Err())
	case <-done:
		result.Output = string(output)
		if err != nil {
			result.Err = fmt.Errorf("remote execution: %w", err)
		}
	}
	return
}

// NewTOFUHostKeyCallback trusts a host on first use and stores its public key.
// A changed key is rejected on subsequent connections.
func NewTOFUHostKeyCallback(path string) (ssh.HostKeyCallback, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("known-hosts path cannot be empty")
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create known-hosts directory: %w", err)
	}
	if file, err := os.OpenFile(path, os.O_CREATE, 0o600); err != nil {
		return nil, fmt.Errorf("create known-hosts file: %w", err)
	} else {
		_ = file.Close()
	}

	var mutex sync.Mutex
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		mutex.Lock()
		defer mutex.Unlock()
		check, err := knownhosts.New(path)
		if err != nil {
			return err
		}
		err = check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyError *knownhosts.KeyError
		if !errors.As(err, &keyError) || len(keyError.Want) > 0 {
			return err
		}
		line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
		file, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if openErr != nil {
			return openErr
		}
		_, writeErr := file.WriteString(line)
		closeErr := file.Close()
		return errors.Join(writeErr, closeErr)
	}, nil
}

func DefaultKnownHostsPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(configDir, "syssetup", "known_hosts"), nil
}
