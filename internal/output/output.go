// Package output renders command results without mixing diagnostics into the
// data stream.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Format controls how a command result is rendered.
type Format string

const (
	FormatAuto     Format = "auto"
	FormatJSON     Format = "json"
	FormatTable    Format = "table"
	FormatMarkdown Format = "markdown"
)

// ParseFormat validates a user-provided output format.
func ParseFormat(value string) (Format, error) {
	format := Format(strings.ToLower(strings.TrimSpace(value)))
	switch format {
	case FormatAuto, FormatJSON, FormatTable, FormatMarkdown:
		return format, nil
	default:
		return "", fmt.Errorf(
			"invalid output format %q (expected auto, json, table, or markdown)",
			value,
		)
	}
}

// RowSource is the stable contract between API response types and human-readable
// renderers. Implementations should return a consistent column order.
//
// Headers and rows are copied before rendering, so a Printer never mutates the
// source's slices.
type RowSource interface {
	OutputHeaders() []string
	OutputRows() [][]string
}

// Table is a convenience RowSource for commands that do not need a dedicated
// response type.
type Table struct {
	Headers []string
	Rows    [][]string
}

// OutputHeaders returns the table's ordered column names.
func (t Table) OutputHeaders() []string {
	return t.Headers
}

// OutputRows returns the table's ordered row values.
func (t Table) OutputRows() [][]string {
	return t.Rows
}

// TerminalDetector reports whether a destination is an interactive terminal.
// Keeping terminal detection injectable makes output selection deterministic in
// tests and lets the command layer choose its preferred terminal library.
type TerminalDetector func(io.Writer) bool

// Printer writes command data to its injected stdout destination. It does not
// own or write to stderr; callers remain responsible for diagnostics.
type Printer struct {
	stdout     io.Writer
	isTerminal TerminalDetector
}

// NewPrinter creates a Printer. A nil terminal detector is treated as
// non-interactive, which keeps piped and test output machine-readable.
func NewPrinter(stdout io.Writer, isTerminal TerminalDetector) *Printer {
	return &Printer{
		stdout:     stdout,
		isTerminal: isTerminal,
	}
}

// Print renders data in the requested format.
//
// Auto selects a table only when stdout is a terminal and data implements
// RowSource. In every other case it emits minified JSON.
func (p *Printer) Print(format Format, data any) error {
	if p == nil {
		return errors.New("output printer is nil")
	}
	if p.stdout == nil {
		return errors.New("output stdout is nil")
	}

	resolved, err := p.resolveFormat(format, data)
	if err != nil {
		return err
	}

	switch resolved {
	case FormatJSON:
		return p.PrintJSON(data)
	case FormatTable:
		return p.printRows(data, renderTable)
	case FormatMarkdown:
		return p.printRows(data, renderMarkdown)
	default:
		return fmt.Errorf("unsupported output format %q", resolved)
	}
}

// PrintJSON emits one minified JSON value followed by a newline.
func (p *Printer) PrintJSON(data any) error {
	if err := p.validateDestination(); err != nil {
		return err
	}
	payload, err := marshalJSON(data)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := p.stdout.Write(payload); err != nil {
		return fmt.Errorf("write json output: %w", err)
	}
	return nil
}

// PrintPrettyJSON emits validated, indented JSON followed by a newline.
func (p *Printer) PrintPrettyJSON(data any) error {
	if err := p.validateDestination(); err != nil {
		return err
	}
	payload, err := marshalInputJSON(data)
	if err != nil {
		return err
	}
	payload, err = PrettyJSON(payload)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := p.stdout.Write(payload); err != nil {
		return fmt.Errorf("write pretty json output: %w", err)
	}
	return nil
}

func (p *Printer) validateDestination() error {
	if p == nil {
		return errors.New("output printer is nil")
	}
	if p.stdout == nil {
		return errors.New("output stdout is nil")
	}
	return nil
}

func (p *Printer) resolveFormat(format Format, data any) (Format, error) {
	if format == "" {
		format = FormatAuto
	}
	switch format {
	case FormatAuto:
		if p.isTerminal != nil && p.isTerminal(p.stdout) {
			if _, ok := data.(RowSource); ok {
				return FormatTable, nil
			}
		}
		return FormatJSON, nil
	case FormatJSON, FormatTable, FormatMarkdown:
		return format, nil
	default:
		return "", fmt.Errorf(
			"invalid output format %q (expected auto, json, table, or markdown)",
			format,
		)
	}
}

func (p *Printer) printRows(data any, render func(io.Writer, []string, [][]string) error) error {
	source, ok := data.(RowSource)
	if !ok {
		return fmt.Errorf("%T does not support tabular output", data)
	}

	headers, rows, err := validatedRows(source)
	if err != nil {
		return err
	}
	if err := render(p.stdout, headers, rows); err != nil {
		return fmt.Errorf("write tabular output: %w", err)
	}
	return nil
}

func validatedRows(source RowSource) ([]string, [][]string, error) {
	headers := append([]string(nil), source.OutputHeaders()...)
	inputRows := source.OutputRows()
	rows := make([][]string, len(inputRows))
	for i, row := range inputRows {
		if len(row) != len(headers) {
			return nil, nil, fmt.Errorf(
				"output row %d has %d columns; expected %d",
				i+1,
				len(row),
				len(headers),
			)
		}
		rows[i] = append([]string(nil), row...)
	}
	return headers, rows, nil
}

func marshalJSON(data any) ([]byte, error) {
	payload, err := marshalInputJSON(data)
	if err != nil {
		return nil, err
	}
	payload, err = MinifyJSON(payload)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func marshalInputJSON(data any) ([]byte, error) {
	switch value := data.(type) {
	case json.RawMessage:
		return append([]byte(nil), value...), nil
	case *json.RawMessage:
		if value == nil {
			return []byte("null"), nil
		}
		return append([]byte(nil), (*value)...), nil
	case []byte:
		return append([]byte(nil), value...), nil
	default:
		payload, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("marshal json output: %w", err)
		}
		return payload, nil
	}
}

// MinifyJSON validates raw JSON and removes insignificant whitespace.
func MinifyJSON(raw []byte) ([]byte, error) {
	if !json.Valid(raw) {
		return nil, errors.New("invalid json output")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, bytes.TrimSpace(raw)); err != nil {
		return nil, fmt.Errorf("minify json output: %w", err)
	}
	return compact.Bytes(), nil
}

// PrettyJSON validates raw JSON and returns deterministic two-space-indented
// output without a trailing newline.
func PrettyJSON(raw []byte) ([]byte, error) {
	if !json.Valid(raw) {
		return nil, errors.New("invalid json output")
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, bytes.TrimSpace(raw), "", "  "); err != nil {
		return nil, fmt.Errorf("pretty-print json output: %w", err)
	}
	return pretty.Bytes(), nil
}

func renderTable(destination io.Writer, headers []string, rows [][]string) error {
	if len(headers) == 0 {
		return nil
	}

	writer := tabwriter.NewWriter(destination, 0, 4, 2, ' ', 0)
	if err := writeTabbedRow(writer, headers); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writeTabbedRow(writer, row); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func writeTabbedRow(destination io.Writer, values []string) error {
	for i, value := range values {
		if i > 0 {
			if _, err := io.WriteString(destination, "\t"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(destination, sanitizeTableCell(value)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(destination, "\n")
	return err
}

func sanitizeTableCell(value string) string {
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}

func renderMarkdown(destination io.Writer, headers []string, rows [][]string) error {
	if len(headers) == 0 {
		return nil
	}

	if err := writeMarkdownRow(destination, headers); err != nil {
		return err
	}
	separators := make([]string, len(headers))
	for i := range separators {
		separators[i] = "---"
	}
	if err := writeMarkdownRow(destination, separators); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writeMarkdownRow(destination, row); err != nil {
			return err
		}
	}
	return nil
}

func writeMarkdownRow(destination io.Writer, values []string) error {
	if _, err := io.WriteString(destination, "| "); err != nil {
		return err
	}
	for i, value := range values {
		if i > 0 {
			if _, err := io.WriteString(destination, " | "); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(destination, escapeMarkdownCell(value)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(destination, " |\n")
	return err
}

func escapeMarkdownCell(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\r", "<br>")
	return strings.ReplaceAll(value, "\n", "<br>")
}
