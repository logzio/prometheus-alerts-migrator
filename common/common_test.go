package common

import (
	"github.com/logzio/logzio_terraform_client/grafana_alerts"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/rulefmt"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestGenerateRandomString(t *testing.T) {
	testCases := []struct {
		name   string
		length int
	}{
		{"length 10", 10},
		{"length 0", 0},
		{"negative length", -1},
		{"large length", 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := GenerateRandomString(tc.length)

			if len(result) != tc.length && tc.length >= 0 {
				t.Errorf("Expected string of length %d, got string of length %d", tc.length, len(result))
			}

			for _, char := range result {
				if !strings.Contains(LetterBytes, string(char)) {
					t.Errorf("generateRandomString() produced a string with invalid character: %v", char)
				}
			}

			if tc.length > 0 {
				otherResult := GenerateRandomString(tc.length)
				if result == otherResult {
					t.Errorf("generateRandomString() does not seem to produce random strings")
				}
			}
		})
	}
}

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
		duration, err := ParseDuration(test.input)
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
	stub := CreateNameStub(cm)
	if stub != expected {
		t.Errorf("Expected %s, got %s", expected, stub)
	}
}

func TestIsAlertEqual(t *testing.T) {
	// dummy time duration
	tenMinutes, _ := model.ParseDuration("10m")
	tenMinutesNs := int64(10 * time.Minute)
	fiveMinutes, _ := model.ParseDuration("5m")

	// dummy expression nodes
	exprNode := yaml.Node{Value: "metric > 0.5"}
	exprQuery := []*grafana_alerts.GrafanaAlertQuery{{Model: map[string]interface{}{"expr": "metric > 0.5"}}}
	differentExprQuery := []*grafana_alerts.GrafanaAlertQuery{{Model: map[string]interface{}{"expr": "metric > 0.7"}}}

	testCases := []struct {
		name        string
		rule        rulefmt.RuleNode
		grafanaRule grafana_alerts.GrafanaAlertRule
		expected    bool
	}{
		{
			name: "same rules",
			rule: rulefmt.RuleNode{
				Alert:       yaml.Node{Value: "SameName"},
				Expr:        exprNode,
				For:         tenMinutes,
				Labels:      map[string]string{"severity": "critical"},
				Annotations: map[string]string{"summary": "High CPU usage"},
			},
			grafanaRule: grafana_alerts.GrafanaAlertRule{
				Title:       "SameName",
				Data:        exprQuery,
				For:         tenMinutesNs,
				Labels:      map[string]string{"severity": "critical"},
				Annotations: map[string]string{"summary": "High CPU usage"},
			},
			expected: true,
		},
		{
			name: "different titles",
			rule: rulefmt.RuleNode{
				Alert: yaml.Node{Value: "AlertName1"},
				Expr:  exprNode,
				For:   tenMinutes,
			},
			grafanaRule: grafana_alerts.GrafanaAlertRule{
				Title: "AlertName2",
				Data:  exprQuery,
				For:   tenMinutesNs,
			},
			expected: false,
		},
		{
			name: "different labels",
			rule: rulefmt.RuleNode{
				Alert:  yaml.Node{Value: "SameName"},
				Expr:   exprNode,
				Labels: map[string]string{"severity": "warning"},
			},
			grafanaRule: grafana_alerts.GrafanaAlertRule{
				Title:  "SameName",
				Labels: map[string]string{"severity": "critical"},
				Data:   exprQuery,
			},
			expected: false,
		},
		{
			name: "different annotations",
			rule: rulefmt.RuleNode{
				Alert:       yaml.Node{Value: "SameName"},
				Expr:        exprNode,
				Annotations: map[string]string{"description": "CPU usage is high"},
			},
			grafanaRule: grafana_alerts.GrafanaAlertRule{
				Title:       "SameName",
				Annotations: map[string]string{"description": "Disk usage is high"},
				Data:        exprQuery,
			},
			expected: false,
		},
		{
			name: "different expressions",
			rule: rulefmt.RuleNode{
				Alert: yaml.Node{Value: "SameName"},
				Expr:  exprNode,
			},
			grafanaRule: grafana_alerts.GrafanaAlertRule{
				Title: "SameName",
				Data:  differentExprQuery,
			},
			expected: false,
		},
		{
			name: "different durations",
			rule: rulefmt.RuleNode{
				Alert: yaml.Node{Value: "SameName"},
				Expr:  exprNode,
				For:   fiveMinutes,
			},
			grafanaRule: grafana_alerts.GrafanaAlertRule{
				Title: "SameName",
				Data:  exprQuery,
				For:   tenMinutesNs,
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAlertEqual(tc.rule, tc.grafanaRule); got != tc.expected {
				t.Errorf("isAlertEqual() for test case %q = %v, want %v", tc.name, got, tc.expected)
			}
		})
	}
}
