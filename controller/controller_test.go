package controller

import (
	"github.com/logzio/logzio_terraform_client/grafana_alerts"
	"github.com/logzio/prometheus-alerts-migrator/common"
	"github.com/prometheus/prometheus/model/rulefmt"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"reflect"
	"testing"
)

const annotation = "test-annotation"

func generateTestController() *Controller {
	cfg, err := common.GetConfig()
	if err != nil {
		klog.Fatalf("Error getting Kubernetes config: %s", err)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err)
	}
	ctlConfig := common.NewConfig()
	kubeInformerFactory := informers.NewSharedInformerFactory(kubeClient, 0)
	c := NewController(kubeClient, kubeInformerFactory.Core().V1().ConfigMaps(), *ctlConfig)
	return c
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

func TestIsRuleConfigMap(t *testing.T) {
	c := generateTestController()
	testCases := []struct {
		name      string
		configMap *v1.ConfigMap
		expected  bool
	}{
		{
			name:      "nil configmap",
			configMap: nil,
			expected:  false,
		},
		{
			name: "configmap without annotations",
			configMap: &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			expected: false,
		},
		{
			name: "configmap with unrelated annotations",
			configMap: &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"unrelated/annotation": "true",
					},
				},
			},
			expected: false,
		},
		{
			name: "configmap with interesting annotation",
			configMap: &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"test-annotation": "true",
					},
				},
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if result := c.isRuleConfigMap(tc.configMap); result != tc.expected {
				t.Errorf("isRuleConfigMap() for %v - got %v, want %v", tc.name, result, tc.expected)
			}
		})
	}
}

func TestHaveConfigMapsChanged(t *testing.T) {
	c := generateTestController()
	// Seed the resourceVersionMap with a known ConfigMap version for comparison.
	knownConfigMap := v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "known",
			Namespace:       "default",
			ResourceVersion: "12345",
			Annotations: map[string]string{
				annotation: "true",
			},
		},
	}
	c.resourceVersionMap[common.CreateNameStub(&knownConfigMap)] = "12345"

	testCases := []struct {
		name     string
		mapList  *v1.ConfigMapList
		expected bool
	}{
		{
			name: "ConfigMapList with unchanged rule ConfigMap",
			mapList: &v1.ConfigMapList{
				Items: []v1.ConfigMap{knownConfigMap},
			},
			expected: false,
		},
		{
			name:     "nil ConfigMapList",
			mapList:  nil,
			expected: false,
		},
		{
			name:     "empty ConfigMapList",
			mapList:  &v1.ConfigMapList{},
			expected: false,
		},
		{
			name: "ConfigMapList with non-rule ConfigMaps",
			mapList: &v1.ConfigMapList{
				Items: []v1.ConfigMap{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "non-rule"},
					},
				},
			},
			expected: false,
		},
		{
			name: "ConfigMapList with new rule ConfigMap",
			mapList: &v1.ConfigMapList{
				Items: []v1.ConfigMap{
					knownConfigMap,
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "new",
							Namespace:       "default",
							ResourceVersion: "67890",
							Annotations: map[string]string{
								annotation: "true",
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "ConfigMapList with changed rule ConfigMap",
			mapList: &v1.ConfigMapList{
				Items: []v1.ConfigMap{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "known",
							Namespace:       "default",
							ResourceVersion: "67890",
							Annotations: map[string]string{
								annotation: "true",
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := c.haveConfigMapsChanged(tc.mapList)
			if result != tc.expected {
				t.Errorf("haveConfigMapsChanged() for %v - got %v, want %v", tc.name, result, tc.expected)
			}
		})
	}
}

func TestCompareAlertRules(t *testing.T) {
	c := generateTestController()
	var data []*grafana_alerts.GrafanaAlertQuery
	dataMap := make(map[string]interface{})
	dataMap["expr"] = "expr"
	dataQuery := &grafana_alerts.GrafanaAlertQuery{
		Model: dataMap,
	}
	data = append(data, dataQuery)
	testCases := []struct {
		name                string
		k8sRulesMap         map[string]rulefmt.RuleNode
		logzioRulesMap      map[string]grafana_alerts.GrafanaAlertRule
		expectedToAddLen    int
		expectedToUpdateLen int
		expectedToDeleteLen int
	}{
		{
			name: "rules to add, update and delete",
			k8sRulesMap: map[string]rulefmt.RuleNode{
				"rule1": {Alert: yaml.Node{Value: "rule1"}}, // Should be added
				"rule2": {Alert: yaml.Node{Value: "rule2"}}, // Should be updated (assuming it's different in Logz.io)
			},
			logzioRulesMap: map[string]grafana_alerts.GrafanaAlertRule{
				"rule2": {Title: "rule2-different"}, // Exists in Kubernetes but is different
				"rule3": {Title: "rule3"},           // Should be deleted (not in Kubernetes)
			},
			expectedToAddLen:    1,
			expectedToUpdateLen: 1,
			expectedToDeleteLen: 1,
		},
		{
			name: "no changes",
			k8sRulesMap: map[string]rulefmt.RuleNode{
				"rule1": {
					Alert: yaml.Node{Value: "rule1"},
					Expr:  yaml.Node{Value: "expr"},
				},
			},
			logzioRulesMap: map[string]grafana_alerts.GrafanaAlertRule{
				"rule1": {
					Title: "rule1",
					Data:  data,
				},
			},
			expectedToAddLen:    0,
			expectedToUpdateLen: 0,
			expectedToDeleteLen: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			toAdd, toUpdate, toDelete := c.compareAlertRules(tc.k8sRulesMap, tc.logzioRulesMap)

			if !reflect.DeepEqual(len(toAdd), tc.expectedToAddLen) {
				t.Errorf("Test %s failed: expected to add %d rules, got %d", tc.name, tc.expectedToAddLen, len(toAdd))
			}
			if !reflect.DeepEqual(len(toUpdate), tc.expectedToUpdateLen) {
				t.Errorf("Test %s failed: expected to update %d rules, got %d", tc.name, tc.expectedToUpdateLen, len(toUpdate))
			}
			if !reflect.DeepEqual(len(toDelete), tc.expectedToDeleteLen) {
				t.Errorf("Test %s failed: expected to delete %d rules, got %d", tc.name, tc.expectedToDeleteLen, len(toDelete))
			}
		})
	}
}
