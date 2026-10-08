package numeric

import "testing"

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name   string
		in     any
		want   float64
		wantOk bool
	}{
		{"float64", float64(3.5), 3.5, true},
		{"float32", float32(2.5), 2.5, true},
		{"int", 42, 42, true},
		{"int64", int64(42), 42, true},
		{"string is not numeric", "42", 0, false},
		{"nil is not numeric", nil, 0, false},
		{"bool is not numeric", true, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ToFloat64(tt.in)
			if ok != tt.wantOk {
				t.Fatalf("ToFloat64(%v) ok = %v, want %v", tt.in, ok, tt.wantOk)
			}
			if ok && got != tt.want {
				t.Errorf("ToFloat64(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
