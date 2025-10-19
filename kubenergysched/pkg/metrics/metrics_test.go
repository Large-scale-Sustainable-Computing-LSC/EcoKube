package metrics

import "testing"

func TestEstimateCarbonKg(t *testing.T) {
	got := EstimateCarbonKg(10, 1.5, 400)
	want := 10 * 1.5 * 0.4
	if got != want {
		t.Fatalf("unexpected carbon estimate: got %f want %f", got, want)
	}
}

func TestNormaliseGuards(t *testing.T) {
	e, c := Normalise(5, 3, 0, 0)
	if e != 5 || c != 3 {
		t.Fatalf("expected defaults to 1, got %f %f", e, c)
	}
}
