package common

import (
	"fmt"
	"github.com/logzio/logzio_terraform_client/grafana_alerts"
	"github.com/logzio/logzio_terraform_client/grafana_contact_points"
	alert_manager_config "github.com/prometheus/alertmanager/config"
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

const (
	LetterBytes   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

// GenerateRandomString borrowed from here https://stackoverflow.com/questions/22892120/how-to-generate-a-random-string-of-a-fixed-length-in-go
func GenerateRandomString(n int) string {
	if n <= 0 {
		return "" // Return an empty string for non-positive lengths
	}
	b := make([]byte, n)
	src := rand.NewSource(time.Now().UnixNano())
	for i, cache, remain := n-1, src.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = src.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(LetterBytes) {
			b[i] = LetterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return string(b)
}

// ParseDuration turns a duration string (example: 5m, 1h) into an int64 value
func ParseDuration(durationStr string) (int64, error) {
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

func CreateNameStub(cm *corev1.ConfigMap) string {
	name := cm.GetObjectMeta().GetName()
	namespace := cm.GetObjectMeta().GetNamespace()

	return fmt.Sprintf("%s-%s", namespace, name)
}

// IsAlertEqual compares two AlertRule objects for equality.
func IsAlertEqual(rule rulefmt.RuleNode, grafanaRule grafana_alerts.GrafanaAlertRule) bool {
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
	forAtt, _ := ParseDuration(rule.For.String())
	if forAtt != grafanaRule.For {
		return false
	}
	if rule.Expr.Value != grafanaRule.Data[0].Model.(map[string]interface{})["expr"] {
		return false
	}
	return true
}

// IsContactPointEqual compares two ContactPoint objects for equality.
func IsContactPointEqual(cp1 alert_manager_config.Receiver, cp2 grafana_contact_points.GrafanaContactPoint) bool {
	if cp1.Name != cp2.Name {
		return false
	}
	// TODO deep comparison
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
