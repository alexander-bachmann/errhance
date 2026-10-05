package errhance

import (
	"strings"
	"testing"
)

func TestContractsWarningFirst(t *testing.T) {
	const src = `package p
import ("io"; "reflect")
func source() error { return io.EOF }
func independent() error { e := source(); if e != nil { return e }; return nil }
func boxed() error { e := source(); if e != nil { return e }; return nil }
func consume(x any, v reflect.Value) {
 _, _ = x.(string)
 _, _ = v.Interface().(interface { Object() })
 e := boxed(); var box any = e
 if box == any(io.EOF) {}
 _, _ = box.(interface { Timeout() bool })
}
type reader struct{}
func (reader) Read(b []byte) (int, error) { return 0, io.EOF }
`
	report, err := Source(NewSourceConfig(), src)
	if err != nil || report.TotalErrorsEnhanced != 1 {
		t.Fatalf("want independent rewrite only: %+v, %v", report, err)
	}
	start, end := strings.Index(report.After, "func boxed()"), strings.Index(report.After, "func consume(")
	if strings.Contains(report.After[start:end], "Errorf") ||
		!strings.Contains(report.After, "return 0, io.EOF") {
		t.Fatalf("known contracts changed:\n%s", report.After)
	}
	for _, d := range report.Diagnostics {
		if d.Position.Line == 7 || d.Position.Line == 8 || strings.Contains(d.Message, "unresolved error identity") {
			t.Errorf("unrelated assertion became error contract: %+v", d)
		}
	}
}

func TestContractsUnrelatedInterfaceFlows(t *testing.T) {
	const prefix = `package p
import "io"
func source() error { return io.EOF }
func producer() error { e := source(); if e != nil { return e }; return nil }
`
	for _, consumer := range []string{
		`func consume(x any) { _, _ = x.(string) }`,
		`func consume(x any) { switch x.(type) { case string: } }`,
		`func consume(x, y any) bool { return x == y }`,
		`func text() string { _ = producer(); return "text" }; func consume() { var x any = text(); _ = x.(string) }`,
		`func pair() (int, error) { return 0, producer() }; func consume() { n, _ := pair(); var x any = n; _ = x.(int) }`,
		`func pair() (int, error) { return 0, producer() }; func consume() { var x any; x, _ = pair(); _ = x.(int) }`,
	} {
		t.Run(consumer, func(t *testing.T) {
			report, err := Source(NewSourceConfig(), prefix+consumer)
			if err != nil || report.TotalErrorsEnhanced != 1 {
				t.Fatalf("unrelated flow interfered: %+v, %v", report, err)
			}
			for _, d := range report.Diagnostics {
				if strings.Contains(d.Message, "wrapping compatibility") {
					t.Errorf("unrelated contract warning: %+v", d)
				}
			}
		})
	}
}

func TestContractsReaderSignatures(t *testing.T) {
	for _, tc := range []struct {
		method string
		reader bool
	}{
		{"Read([]byte) (int, error)", true},
		{"ReadByte() (byte, error)", true},
		{"ReadRune() (rune, int, error)", true},
		{"Read(string) (int, error)", false},
		{"Read(...byte) (int, error)", false},
		{"Read([]byte) (int64, error)", false},
		{"ReadByte(int) (byte, error)", false},
		{"ReadByte() (int, error)", false},
		{"ReadRune() (byte, int, error)", false},
	} {
		t.Run(tc.method, func(t *testing.T) {
			results := "0, producer()"
			if strings.HasPrefix(tc.method, "ReadRune") {
				results = "0, 0, producer()"
			}
			src := `package p
import "io"
func source() error { return io.EOF }
func producer() error { e := source(); if e != nil { return e }; return nil }
func independent() error { e := source(); if e != nil { return e }; return nil }
type reader struct{}
func (reader) ` + tc.method + ` { return ` + results + ` }`
			report, err := Source(NewSourceConfig(), src)
			want := 2
			if tc.reader {
				want = 1
			}
			if err != nil || report.TotalErrorsEnhanced != want {
				t.Fatalf("want %d rewrites: %+v, %v", want, report, err)
			}
		})
	}
}

func TestContractsMixedKnownAndUnknown(t *testing.T) {
	const src = `package p
import "io"
func source() error { return io.EOF }
func producer() error { e := source(); if e != nil { return e }; return nil }
func independent() error { e := source(); if e != nil { return e }; return nil }
func consume(unknown error, flag bool) {
 e := producer()
 if flag { e = unknown }
 if e == io.EOF {}
}`
	report, err := Source(NewSourceConfig(), src)
	if err != nil || report.TotalErrorsEnhanced != 1 {
		t.Fatalf("want unrelated producer rewritten: %+v, %v", report, err)
	}
	start, end := strings.Index(report.After, "func producer()"), strings.Index(report.After, "func independent()")
	if strings.Contains(report.After[start:end], "Errorf") {
		t.Fatalf("known producer rewritten:\n%s", report.After)
	}
	for _, d := range report.Diagnostics {
		if strings.Contains(d.Message, "unresolved error identity contract") {
			return
		}
	}
	t.Fatalf("missing uncertainty warning: %+v", report.Diagnostics)
}
