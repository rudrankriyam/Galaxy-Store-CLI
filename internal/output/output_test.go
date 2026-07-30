package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestHumanReadableOutputNeutralizesTerminalControls(t *testing.T) {
	t.Parallel()

	table := Table{
		Headers: []string{
			"unsafe\x1b]0;title\x07\u202e\tcolumn",
			"Note",
		},
		Rows: [][]string{{
			"before\x1b[31mred\x1b[0m\u009b2K\u009d0;osc\u009c" +
				"\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e" +
				"\u2066\u2067\u2068\u2069after\x7f",
			"first\nsecond\r\nthird\rfour\tfive\u0085six\u2028seven\u2029eight",
		}},
	}

	t.Run("table", func(t *testing.T) {
		t.Parallel()

		var stdout bytes.Buffer
		if err := NewPrinter(&stdout, nil).Print(FormatTable, table); err != nil {
			t.Fatalf("Print(table): %v", err)
		}
		output := stdout.String()
		assertInertHumanOutput(t, output)
		if got, want := strings.Count(output, "\n"), 2; got != want {
			t.Fatalf("table line count = %d, want %d; output %q", got, want, output)
		}
		for _, value := range []string{
			"unsafe]0;title column",
			"before[31mred[0m2K0;oscafter",
			"first second third four five six seven eight",
		} {
			if !strings.Contains(output, value) {
				t.Fatalf("table output = %q, want sanitized value %q", output, value)
			}
		}
	})

	t.Run("markdown", func(t *testing.T) {
		t.Parallel()

		var stdout bytes.Buffer
		if err := NewPrinter(&stdout, nil).Print(FormatMarkdown, table); err != nil {
			t.Fatalf("Print(markdown): %v", err)
		}
		output := stdout.String()
		assertInertHumanOutput(t, output)
		if got, want := strings.Count(output, "\n"), 3; got != want {
			t.Fatalf("markdown line count = %d, want %d; output %q", got, want, output)
		}
		for _, value := range []string{
			"unsafe]0;title column",
			"before[31mred[0m2K0;oscafter",
			"first<br>second<br>third<br>four five six seven eight",
		} {
			if !strings.Contains(output, value) {
				t.Fatalf("markdown output = %q, want sanitized value %q", output, value)
			}
		}
	})
}

func TestHumanReadableSanitizationPreservesJSONValues(t *testing.T) {
	t.Parallel()

	original := Table{
		Headers: []string{"unsafe\x1b]0;title\x07\u202e"},
		Rows: [][]string{{
			"line one\nline two\t\u009b2K\u2066hidden\u2069",
		}},
	}
	var stdout bytes.Buffer
	if err := NewPrinter(&stdout, nil).Print(FormatJSON, original); err != nil {
		t.Fatalf("Print(json): %v", err)
	}
	expectedBytes, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal expected JSON: %v", err)
	}
	expectedBytes = append(expectedBytes, '\n')
	if !bytes.Equal(stdout.Bytes(), expectedBytes) {
		t.Fatalf("JSON bytes = %q, want exact existing encoding %q", stdout.Bytes(), expectedBytes)
	}

	var decoded Table
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("JSON value = %#v, want exact original %#v", decoded, original)
	}
}

func TestSanitizeHumanCellCoversControlRangesAndInvalidUTF8(t *testing.T) {
	t.Parallel()

	var hostile strings.Builder
	hostile.WriteString("before")
	for control := rune(0); control <= 0x1f; control++ {
		hostile.WriteRune(control)
	}
	hostile.WriteRune(0x7f)
	for control := rune(0x80); control <= 0x9f; control++ {
		hostile.WriteRune(control)
	}
	for _, bidi := range []rune{
		0x061c,
		0x200e, 0x200f,
		0x202a, 0x202b, 0x202c, 0x202d, 0x202e,
		0x2066, 0x2067, 0x2068, 0x2069,
	} {
		hostile.WriteRune(bidi)
	}
	hostile.WriteString("after")
	hostile.WriteByte(0xc2)
	hostile.WriteString("suffix")

	got := sanitizeHumanCell(hostile.String(), " ")
	assertInertHumanOutput(t, got)
	for _, value := range []string{"before", "after", "\ufffdsuffix"} {
		if !strings.Contains(got, value) {
			t.Fatalf("sanitizeHumanCell output = %q, want %q preserved", got, value)
		}
	}
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

func assertInertHumanOutput(t *testing.T, output string) {
	t.Helper()

	for index := 0; index < len(output); {
		r, size := utf8.DecodeRuneInString(output[index:])
		if r == utf8.RuneError && size == 1 {
			t.Fatalf("human output contains invalid UTF-8 at byte %d: %q", index, output)
		}
		if r == '\n' {
			index += size
			continue
		}
		switch {
		case r < 0x20, r == 0x7f:
			t.Fatalf("human output contains C0/DEL control %U: %q", r, output)
		case r >= 0x80 && r <= 0x9f:
			t.Fatalf("human output contains C1 control %U: %q", r, output)
		case r == 0x061c, r == 0x200e, r == 0x200f:
			t.Fatalf("human output contains bidi mark %U: %q", r, output)
		case r >= 0x202a && r <= 0x202e:
			t.Fatalf("human output contains bidi embedding/override %U: %q", r, output)
		case r >= 0x2066 && r <= 0x2069:
			t.Fatalf("human output contains bidi isolate %U: %q", r, output)
		case r == 0x2028, r == 0x2029:
			t.Fatalf("human output contains Unicode line separator %U: %q", r, output)
		}
		index += size
	}
}
