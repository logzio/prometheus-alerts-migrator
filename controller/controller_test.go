package controller

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		err      bool
	}{
		{"", 0, true},
		{"123", 123 * int64(time.Second), false},
		{"1h", int64(time.Hour), false},
		{"invalid", 0, true},
	}

	for _, test := range tests {
		duration, err := parseDuration(test.input)
		if test.err && err == nil {
			t.Errorf("Expected error for input %s", test.input)
		}
		if !test.err && duration != test.expected {
			t.Errorf("Expected %d, got %d for input %s", test.expected, duration, test.input)
		}
	}
}

func TestCreateNameStub(t *testing.T) {
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-name",
			Namespace: "test-namespace",
		},
	}
	expected := "test-namespace-test-name"
	stub := createNameStub(cm)
	if stub != expected {
		t.Errorf("Expected %s, got %s", expected, stub)
	}
}
