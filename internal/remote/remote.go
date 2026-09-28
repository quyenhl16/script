package remote

import (
	"bytes"
	"context"
	"encoding/base64"
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
	Servers    []Server
	Command    string
	Script     []byte
	ScriptArgs []string
	Artifacts  []Artifact
	Timeout    time.Duration
	HostKeys   ssh.HostKeyCallback
}

type Artifact struct {
	ID   string
	Data []byte
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
	if len(request.Artifacts) > 0 {
		prepared, err := prepareScript(request.Script, request.Artifacts)
		if err != nil {
			for index, server := range request.Servers {
				results[index] = Result{Address: server.Address, Err: err}
			}
			return results
		}
		request.Script = prepared
		request.Artifacts = nil
	}
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

// FetchDirectory runs one SSH command on the server that streams the
// base64-encoded tar.gz of a remote directory back over stdout. The caller
// decodes and extracts it locally so remote run artifacts (reports, annotated
// workbooks) land on the machine the tool runs on, not only on the server.
// A missing directory is reported as (nil, nil): the step may legitimately
// produce no output (e.g. halted before its checker ran).
func FetchDirectory(ctx context.Context, server Server, directory string, timeout time.Duration, hostKeys ssh.HostKeyCallback) ([]byte, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, errors.New("remote directory cannot be empty")
	}
	if hostKeys == nil {
		return nil, errors.New("host key verifier is required")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", server.Address)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	config := &ssh.ClientConfig{
		User:            server.User,
		Auth:            []ssh.AuthMethod{ssh.Password(server.Password)},
		HostKeyCallback: hostKeys,
		Timeout:         timeout,
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, server.Address, config)
	if err != nil {
		return nil, fmt.Errorf("SSH handshake: %w", err)
	}
	client := ssh.NewClient(clientConnection, channels, requests)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()
	command := fmt.Sprintf("if [ -d %s ]; then tar -czf - -C $(dirname %s) $(basename %s) | base64 -w 76; fi",
		quoteShellArgument(filepath.Clean(directory)),
		quoteShellArgument(filepath.Clean(directory)),
		quoteShellArgument(filepath.Clean(directory)))
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Run(command); err != nil {
		return nil, fmt.Errorf("fetch %s: %w (%s)", directory, err, strings.TrimSpace(stderr.String()))
	}
	encoded := strings.TrimSpace(stdout.String())
	if encoded == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode %s payload: %w", directory, err)
	}
	return decoded, nil
}

func LoadArtifact(id, path string) (Artifact, error) {
	if _, err := artifactEnvironmentName(id); err != nil {
		return Artifact{}, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return Artifact{}, errors.New("artifact source path cannot be empty")
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Artifact{}, fmt.Errorf("open artifact %q: %w", id, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Artifact{}, fmt.Errorf("inspect artifact %q: %w", id, err)
	}
	if !info.Mode().IsRegular() {
		return Artifact{}, fmt.Errorf("artifact %q must be a regular file", id)
	}
	const maxArtifactSize = 10 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxArtifactSize+1))
	if err != nil {
		return Artifact{}, fmt.Errorf("read artifact %q: %w", id, err)
	}
	if len(data) > maxArtifactSize {
		return Artifact{}, fmt.Errorf("artifact %q is larger than 10 MiB", id)
	}
	return Artifact{ID: id, Data: data}, nil
}

func prepareScript(script []byte, artifacts []Artifact) ([]byte, error) {
	if len(artifacts) == 0 {
		return script, nil
	}
	if len(script) == 0 {
		return nil, errors.New("artifacts require a script")
	}
	const maxArtifacts = 32
	if len(artifacts) > maxArtifacts {
		return nil, fmt.Errorf("at most %d artifacts are allowed", maxArtifacts)
	}
	var payload strings.Builder
	payload.WriteString("set -Eeuo pipefail\n")
	payload.WriteString("command -v base64 >/dev/null 2>&1 || { echo 'Error: base64 command was not found' >&2; exit 1; }\n")
	payload.WriteString("umask 077\n")
	payload.WriteString("syssetup_artifact_dir=\"$(mktemp -d)\"\n")
	payload.WriteString("trap 'rm -rf -- \"$syssetup_artifact_dir\"' EXIT\n")
	seen := make(map[string]bool)
	totalSize := 0
	for index, artifact := range artifacts {
		environmentName, err := artifactEnvironmentName(artifact.ID)
		if err != nil {
			return nil, err
		}
		if seen[environmentName] {
			return nil, fmt.Errorf("duplicate artifact ID %q", artifact.ID)
		}
		seen[environmentName] = true
		totalSize += len(artifact.Data)
		if totalSize > 20<<20 {
			return nil, errors.New("total artifact size is larger than 20 MiB")
		}
		marker := fmt.Sprintf("__SYSSETUP_ARTIFACT_%d__", index)
		remoteName := fmt.Sprintf("artifact-%d", index)
		fmt.Fprintf(&payload, "base64 -d >\"$syssetup_artifact_dir/%s\" <<'%s'\n", remoteName, marker)
		payload.WriteString(base64.StdEncoding.EncodeToString(artifact.Data))
		payload.WriteByte('\n')
		payload.WriteString(marker)
		payload.WriteByte('\n')
		fmt.Fprintf(&payload, "export %s=\"$syssetup_artifact_dir/%s\"\n", environmentName, remoteName)
	}
	payload.WriteString("\n(\n")
	payload.Write(script)
	payload.WriteString("\n)\n")
	return []byte(payload.String()), nil
}

func artifactEnvironmentName(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("artifact ID cannot be empty")
	}
	for index, character := range id {
		valid := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-'
		if !valid || index == 0 && character >= '0' && character <= '9' {
			return "", fmt.Errorf("invalid artifact ID %q", id)
		}
	}
	normalized := strings.ToUpper(strings.ReplaceAll(id, "-", "_"))
	return "SYSSETUP_ARTIFACT_" + normalized, nil
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
	if len(request.Script) == 0 && len(request.ScriptArgs) > 0 {
		result.Err = errors.New("script arguments require a script")
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
		command = "bash -s --"
		for _, argument := range request.ScriptArgs {
			if strings.ContainsRune(argument, 0) {
				result.Err = errors.New("script argument contains a null byte")
				return
			}
			command += " " + quoteShellArgument(argument)
		}
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

func quoteShellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
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