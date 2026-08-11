package runner

import (
	"bytes"
	"io"
	"testing"
)

func TestStatusColorWriterColorsPassAndFailLines(t *testing.T) {
	var output bytes.Buffer
	writer := newStatusColorWriter(&output, true)

	for _, chunk := range []string{
		"plain line\n  [PA",
		"SS] service is ready\n",
		"  [FAIL] service is down\r\npartial [PASS]",
	} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}

	want := "plain line\n" +
		ansiPass + "  [PASS] service is ready" + ansiReset + "\n" +
		ansiFail + "  [FAIL] service is down" + ansiReset + "\r\n" +
		ansiPass + "partial [PASS]" + ansiReset
	if output.String() != want {
		t.Fatalf("unexpected colored output:\n got: %q\nwant: %q", output.String(), want)
	}
}

func TestStatusColorWriterCanDisableColors(t *testing.T) {
	var output bytes.Buffer
	writer := newStatusColorWriter(&output, false)
	input := "[PASS] ready\n[FAIL] broken\n"

	if _, err := writer.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if output.String() != input {
		t.Fatalf("disabled writer changed output: %q", output.String())
	}
}

func TestStatusColorWriterKeepsLogOutputPlain(t *testing.T) {
	var display bytes.Buffer
	var log bytes.Buffer
	writer := newStatusColorWriter(&display, true)
	output := io.MultiWriter(writer, &log)
	input := "[PASS] ready\n[FAIL] broken\n"

	if _, err := output.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if display.String() == input {
		t.Fatal("display output was not colored")
	}
	if log.String() != input {
		t.Fatalf("log output contains display formatting: %q", log.String())
	}
}
