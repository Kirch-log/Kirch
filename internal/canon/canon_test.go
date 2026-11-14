package canon

import (
	"strings"
	"testing"
)

func TestApplies(t *testing.T) {
	for p, want := range map[string]bool{
		"netlist.json":         true,
		"boards/a/layout.json": true,
		"capture.json":         true,
		"firmware/main.cpp":    false,
		"notes.json":           false,
	} {
		if Applies(p) != want {
			t.Errorf("Applies(%q) = %v", p, !want)
		}
	}
}

func TestFormatSortsKeysAndPins(t *testing.T) {
	in := `{"nets":{"SERVO_SIG":["U1.GPIO9","SERVO1.SIG"],"GND":["U1.GND","SERVO1.GND"]},"format":"kirch.netlist/1"}`
	want := `{
  "format": "kirch.netlist/1",
  "nets": {
    "GND": [
      "SERVO1.GND",
      "U1.GND"
    ],
    "SERVO_SIG": [
      "SERVO1.SIG",
      "U1.GPIO9"
    ]
  }
}
`
	got, err := Format("netlist.json", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// Same circuit written two different ways → identical bytes.
func TestSameCircuitSameBytes(t *testing.T) {
	a := `{"format":"kirch.parts/1","parts":{"R1":{"type":"resistor","value":"1k"},"U1":{"type":"mcu"}}}`
	b := "{\n \"parts\": {\"U1\": {\"type\": \"mcu\"},\n  \"R1\": {\"value\": \"1k\", \"type\": \"resistor\"}},\n \"format\": \"kirch.parts/1\"}\n\n"
	fa, err := Format("parts.json", []byte(a))
	if err != nil {
		t.Fatal(err)
	}
	fb, err := Format("parts.json", []byte(b))
	if err != nil {
		t.Fatal(err)
	}
	if string(fa) != string(fb) {
		t.Fatalf("differ:\n%s\n---\n%s", fa, fb)
	}
}

func TestFormatIsStable(t *testing.T) {
	in := `{"format":"kirch.capture/1","probes":{"P1":{"mv":{"avg":4975,"max":5010,"min":4920}}},"window_ms":1000}`
	once, err := Format("capture.json", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Format("capture.json", once)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Fatal("formatting canonical output changed it")
	}
}

func TestNumbersKeptExactly(t *testing.T) {
	got, err := Format("capture.json", []byte(`{"a":4975,"b":1.50,"c":12345678901234567890}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"4975", "1.50", "12345678901234567890"} {
		if !strings.Contains(string(got), n) {
			t.Errorf("%s lost in %s", n, got)
		}
	}
}

func TestMixedListsKeepOrder(t *testing.T) {
	got, err := Format("layout.json", []byte(`{"path":[3,1,2],"mix":["b",1,"a"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "3,\n    1,\n    2") {
		t.Errorf("number list reordered: %s", got)
	}
}

func TestNoHTMLEscaping(t *testing.T) {
	got, err := Format("parts.json", []byte(`{"notes":"R1 <-> R2 & C1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "R1 <-> R2 & C1") {
		t.Errorf("escaped: %s", got)
	}
}

func TestRejects(t *testing.T) {
	cases := map[string]struct{ name, in, msg string }{
		"bad json":       {"netlist.json", `{"nets":`, "not valid JSON"},
		"not object":     {"netlist.json", `[1,2]`, "must be a JSON object"},
		"trailing data":  {"parts.json", `{} {}`, "extra data"},
		"wrong format":   {"netlist.json", `{"format":"kirch.parts/1"}`, "expected kirch.netlist/"},
		"pin in 2 nets":  {"netlist.json", `{"nets":{"A":["U1.1"],"B":["U1.1"]}}`, "U1.1 is in two nets"},
		"pin twice":      {"netlist.json", `{"nets":{"A":["U1.1","U1.1"]}}`, "listed twice"},
		"pin no dot":     {"netlist.json", `{"nets":{"A":["GPIO9"]}}`, "PART.PIN"},
		"nets not obj":   {"netlist.json", `{"nets":["A"]}`, `"nets" must be an object`},
		"net not a list": {"netlist.json", `{"nets":{"A":"U1.1"}}`, "must be a list"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Format(c.name, []byte(c.in))
			if err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("err = %v, want containing %q", err, c.msg)
			}
		})
	}
}
