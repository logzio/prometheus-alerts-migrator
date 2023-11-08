package controller

import (
	"fmt"
	"github.com/logzio/logzio_terraform_client/grafana_alerts"
	"github.com/prometheus/prometheus/model/rulefmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"time"
)

// borrowed from here https://stackoverflow.com/questions/22892120/how-to-generate-a-random-string-of-a-fixed-length-in-go
func generateRandomString(n int) string {
	b := make([]byte, n)
	src := rand.NewSource(time.Now().UnixNano())
	for i, cache, remain := n-1, src.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = src.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return string(b)
}

func parseDuration(durationStr string) (int64, error) {
	// Check if the string is empty
	if durationStr == "" {
		return 0, fmt.Errorf("duration string is empty")
	}

	// Handle the special case where the duration string is just a number (assumed to be seconds)
	if _, err := strconv.Atoi(durationStr); err == nil {
		seconds, _ := strconv.ParseInt(durationStr, 10, 64)
		return seconds * int64(time.Second), nil
	}

	// Parse the duration string
	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return 0, err
	}

	// Convert the time.Duration value to an int64
	return int64(duration), nil
}

func createNameStub(cm *corev1.ConfigMap) string {
	name := cm.GetObjectMeta().GetName()
	namespace := cm.GetObjectMeta().GetNamespace()

	return fmt.Sprintf("%s-%s", namespace, name)
}

// isAlertEqual compares two AlertRule objects for equality.
// You should expand this function to compare all relevant fields of AlertRule.
func isAlertEqual(rule rulefmt.RuleNode, grafanaRule grafana_alerts.GrafanaAlertRule) bool {
	// Start with name comparison; if these don't match, they're definitely not equal.
	if rule.Alert.Value != grafanaRule.Title {
		return false
	}
	if !reflect.DeepEqual(rule.Labels, grafanaRule.Labels) {
		return false
	}
	if !reflect.DeepEqual(rule.Annotations, grafanaRule.Annotations) {
		return false
	}
	forAtt, _ := parseDuration(rule.For.String())
	if forAtt != grafanaRule.For {
		return false
	}
	if rule.Expr.Value != grafanaRule.Data[0].Model.(map[string]interface{})["expr"] {
		return false
	}
	return true
}

// GetConfig returns a Kubernetes config
func GetConfig() (*rest.Config, error) {
	var config *rest.Config

	kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
	if _, err := os.Stat(kubeconfig); err == nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
	}

	return config, nil
}
