package controller

import (
	"github.com/logzio/logzio_terraform_client/grafana_alerts"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/rulefmt"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	"reflect"
	"testing"
	"time"
)

func generateTestController() *Controller {
	cfg, err := GetConfig()
	if err != nil {
		klog.Fatalf("Error getting Kubernetes config: %s", err)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err)
	}
	kubeInformerFactory := informers.NewSharedInformerFactory(kubeClient, 0)
	annotation := "anno"
	c := NewController(kubeClient, kubeInformerFactory.Core().V1().ConfigMaps(), &annotation, "token", "url", "ds", "env")
	return c
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
			if got := isAlertEqual(tc.rule, tc.grafanaRule); got != tc.expected {
				t.Errorf("isAlertEqual() for test case %q = %v, want %v", tc.name, got, tc.expected)
			}
		})
	}
}

func TestGenerateGrafanaAlert(t *testing.T) {
	ctrl := generateTestController()

	// Define common rule parts for reuse in test cases
	baseRule := rulefmt.RuleNode{
		Alert:       yaml.Node{Value: "TestAlert"},
		Expr:        yaml.Node{Value: "up == 1"},
		For:         model.Duration(5 * time.Minute),
		Labels:      map[string]string{"severity": "critical"},
		Annotations: map[string]string{"description": "Instance is down"},
	}
	baseFolderUid := "folder123"

	// Test cases
	testCases := []struct {
		name      string
		rule      rulefmt.RuleNode
		folderUid string
		wantErr   bool
	}{
		{
			name:      "valid conversion with annotations and labels",
			rule:      baseRule, // Already has annotations and labels
			folderUid: baseFolderUid,
			wantErr:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alertRule, err := ctrl.generateGrafanaAlert(tc.rule, tc.folderUid)

			// Check for unexpected errors or lack thereof
			if (err != nil) != tc.wantErr {
				t.Errorf("generateGrafanaAlert() error = %v, wantErr %v", err, tc.wantErr)
				return // Skip further checks if there's an unexpected error
			}
			if !tc.wantErr {
				// Validate Title
				if alertRule.Title != tc.rule.Alert.Value {
					t.Errorf("generateGrafanaAlert() Title = %v, want %v", alertRule.Title, tc.rule.Alert.Value)
				}

				// Validate FolderUID
				if alertRule.FolderUID != tc.folderUid {
					t.Errorf("generateGrafanaAlert() FolderUID = %v, want %v", alertRule.FolderUID, tc.folderUid)
				}

				// Validate Labels
				if !reflect.DeepEqual(alertRule.Labels, tc.rule.Labels) {
					t.Errorf("generateGrafanaAlert() Labels = %v, want %v", alertRule.Labels, tc.rule.Labels)
				}

				// Validate Annotations
				if !reflect.DeepEqual(alertRule.Annotations, tc.rule.Annotations) {
					t.Errorf("generateGrafanaAlert() Annotations = %v, want %v", alertRule.Annotations, tc.rule.Annotations)
				}
			}
		})
	}
}

func TestExtractValues(t *testing.T) {
	c := generateTestController()
	// Define test cases
	testCases := []struct {
		name          string
		configMap     *v1.ConfigMap
		expectedRules int
	}{
		{
			name: "valid configmap with one rule",
			configMap: &v1.ConfigMap{
				Data: map[string]string{
					"rule1": "alert: HighLatency\nexpr: job:request_latency_seconds:mean5m{job=\"myjob\"} > 0.5\nfor: 10m\n",
				},
			},
			expectedRules: 1,
		},
		{
			name: "valid configmap with multiple rules",
			configMap: &v1.ConfigMap{
				Data: map[string]string{
					"rule1": "alert: HighLatency\nexpr: job:request_latency_seconds:mean5m{job=\"myjob\"} > 0.5\nfor: 10m\n",
					"rule2": "alert: HighErrors\nexpr: job:errors:rate5m{job=\"myjob\"} > 5\nfor: 10m\n",
				},
			},
			expectedRules: 2,
		},
		{
			name: "configmap with invalid rule data",
			configMap: &v1.ConfigMap{
				Data: map[string]string{
					"invalid_rule": "this is not a valid prometheus rule",
				},
			},
			expectedRules: 0,
		},
		{
			name: "configmap with one invalid rule and one valid rule",
			configMap: &v1.ConfigMap{
				Data: map[string]string{
					"invalid_rule": "this is not a valid prometheus rule",
					"rule1":        "alert: HighLatency\nexpr: job:request_latency_seconds:mean5m{job=\"myjob\"} > 0.5\nfor: 10m\n",
				},
			},
			expectedRules: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rules := c.extractValues(tc.configMap)
			if len(rules) != tc.expectedRules {
				t.Errorf("extractValues() for %v - expected %d rules, got %d", tc.name, tc.expectedRules, len(rules))
			}
		})
	}
}
