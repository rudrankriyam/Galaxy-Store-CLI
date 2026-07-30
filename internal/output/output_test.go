package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  Format
	}{
		{input: "auto", want: FormatAuto},
		{input: " JSON ", want: FormatJSON},
		{input: "table", want: FormatTable},
		{input: "markdown", want: FormatMarkdown},
	}
	for _, test := range tests {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			got, err := ParseFormat(test.input)
			if err != nil {
				t.Fatalf("ParseFormat(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("ParseFormat(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}

	if _, err := ParseFormat("yaml"); err == nil {
		t.Fatal("ParseFormat(yaml) unexpectedly succeeded")
	}
}

func TestPrinterAutoOutput(t *testing.T) {
	t.Parallel()

	table := Table{
		Headers: []string{"ID", "Name"},
		Rows: [][]string{
			{"123", "Sample App"},
		},
	}

	t.Run("terminal uses table", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		printer := NewPrinter(&stdout, func(io.Writer) bool { return true })
		if err := printer.Print(FormatAuto, table); err != nil {
			t.Fatalf("Print(auto): %v", err)
		}
		if got, want := stdout.String(), "ID   Name\n123  Sample App\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("pipe uses minified json", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		printer := NewPrinter(&stdout, func(io.Writer) bool { return false })
		if err := printer.Print(FormatAuto, table); err != nil {
			t.Fatalf("Print(auto): %v", err)
		}
		want := `{"Headers":["ID","Name"],"Rows":[["123","Sample App"]]}` + "\n"
		if got := stdout.String(); got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("terminal without rows uses json", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		printer := NewPrinter(&stdout, func(io.Writer) bool { return true })
		if err := printer.Print(FormatAuto, map[string]string{"id": "123"}); err != nil {
			t.Fatalf("Print(auto): %v", err)
		}
		if got, want := stdout.String(), "{\"id\":\"123\"}\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})
}

func TestPrinterExplicitFormats(t *testing.T) {
	t.Parallel()

	table := Table{
		Headers: []string{"Name", "Note"},
		Rows: [][]string{
			{"Sample | App", "first\nline"},
		},
	}

	t.Run("json remains minified on terminal", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		printer := NewPrinter(&stdout, func(io.Writer) bool { return true })
		if err := printer.Print(FormatJSON, map[string]any{"name": "Sample", "count": 1}); err != nil {
			t.Fatalf("Print(json): %v", err)
		}
		if got, want := stdout.String(), "{\"count\":1,\"name\":\"Sample\"}\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("table sanitizes layout characters", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		printer := NewPrinter(&stdout, nil)
		if err := printer.Print(FormatTable, table); err != nil {
			t.Fatalf("Print(table): %v", err)
		}
		want := "Name          Note\nSample | App  first line\n"
		if got := stdout.String(); got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("markdown escapes cells", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		printer := NewPrinter(&stdout, nil)
		if err := printer.Print(FormatMarkdown, table); err != nil {
			t.Fatalf("Print(markdown): %v", err)
		}
		want := strings.Join([]string{
			"| Name | Note |",
			"| --- | --- |",
			"| Sample \\| App | first<br>line |",
			"",
		}, "\n")
		if got := stdout.String(); got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})
}

func TestRawJSONValidationAndFormatting(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(" { \"name\": \"Sample\", \"items\": [1, 2] } ")

	t.Run("minified", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		printer := NewPrinter(&stdout, nil)
		if err := printer.PrintJSON(raw); err != nil {
			t.Fatalf("PrintJSON: %v", err)
		}
		if got, want := stdout.String(), "{\"name\":\"Sample\",\"items\":[1,2]}\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("pretty", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		printer := NewPrinter(&stdout, nil)
		if err := printer.PrintPrettyJSON(raw); err != nil {
			t.Fatalf("PrintPrettyJSON: %v", err)
		}
		want := "{\n  \"name\": \"Sample\",\n  \"items\": [\n    1,\n    2\n  ]\n}\n"
		if got := stdout.String(); got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("invalid json is rejected before writing", func(t *testing.T) {
		t.Parallel()
		var stdout bytes.Buffer
		printer := NewPrinter(&stdout, nil)
		if err := printer.PrintPrettyJSON(json.RawMessage("{")); err == nil {
			t.Fatal("PrintPrettyJSON(invalid) unexpectedly succeeded")
		}
		if stdout.Len() != 0 {
			t.Fatalf("invalid JSON wrote %q", stdout.String())
		}
	})
}

func TestPrinterRejectsInvalidTabularDataBeforeWriting(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	printer := NewPrinter(&stdout, nil)
	err := printer.Print(FormatTable, Table{
		Headers: []string{"ID", "Name"},
		Rows:    [][]string{{"123"}},
	})
	if err == nil {
		t.Fatal("Print(table) unexpectedly succeeded")
	}
	if stdout.Len() != 0 {
		t.Fatalf("invalid table wrote %q", stdout.String())
	}
}

func TestPrinterReturnsWriterError(t *testing.T) {
	t.Parallel()

	printer := NewPrinter(errorWriter{}, nil)
	err := printer.Print(FormatJSON, map[string]string{"id": "123"})
	if err == nil {
		t.Fatal("Print(json) unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "write json output") {
		t.Fatalf("error = %q", err)
	}
}

func TestPrinterRequiresDestination(t *testing.T) {
	t.Parallel()

	var nilPrinter *Printer
	if err := nilPrinter.PrintJSON(nil); err == nil {
		t.Fatal("nil Printer.PrintJSON unexpectedly succeeded")
	}
	if err := NewPrinter(nil, nil).PrintPrettyJSON(nil); err == nil {
		t.Fatal("Printer with nil stdout unexpectedly succeeded")
	}
}

func TestPrinterDoesNotMutateRows(t *testing.T) {
	t.Parallel()

	headers := []string{"Name"}
	rows := [][]string{{"line 1\nline 2"}}
	table := Table{Headers: headers, Rows: rows}
	var stdout bytes.Buffer
	if err := NewPrinter(&stdout, nil).Print(FormatTable, table); err != nil {
		t.Fatalf("Print(table): %v", err)
	}

	if got, want := headers[0], "Name"; got != want {
		t.Fatalf("header mutated to %q", got)
	}
	if got, want := rows[0][0], "line 1\nline 2"; got != want {
		t.Fatalf("row mutated to %q", got)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
