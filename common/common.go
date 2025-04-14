package common

import (
	"flag"
	"fmt"
	grafanaalerts "github.com/logzio/logzio_terraform_client/grafana_alerts"
	"github.com/prometheus/prometheus/model/rulefmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"
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
	letterIdxMax  = 63 / letterIdxBits
	TypeSlack     = "slack"
	TypeEmail     = "email"
	TypePagerDuty = "pagerduty" // # of letter indices fitting in 63 bits
	TypeMsTeams   = "teams"
)

var (
	helpFlag, ignoreSlackTextFlag, ignoreSlackTitleFlag                                                                     *bool
	logzioAPITokenFlag, rulesConfigmapAnnotation, alertManagerConfigmapAnnotation, logzioAPIURLFlag, rulesDSFlag, envIDFlag *string
	workerCountFlag                                                                                                         *int
)

// NewConfig creates a Config struct, populating it with values from command-line flags and environment variables.
func NewConfig() *Config {
	// Define flags
	if flag.Lookup("help") == nil {
		helpFlag = flag.Bool("help", false, "Display help")
	}
	if flag.Lookup("rules-annotation") == nil {
		rulesConfigmapAnnotation = flag.String("rules-annotation", "prometheus.io/kube-rules", "Annotation that states that this configmap contains prometheus rules")
	}
	if flag.Lookup("alertmanager-annotation") == nil {
		alertManagerConfigmapAnnotation = flag.String("alertmanager-annotation", "prometheus.io/kube-alertmanager", "Annotation that states that this configmap contains alertmanager configuration")
	}
	if flag.Lookup("logzio-api-token") == nil {
		logzioAPITokenFlag = flag.String("logzio-api-token", "", "LOGZIO API token")
	}
	if flag.Lookup("logzio-api-url") == nil {
		logzioAPIURLFlag = flag.String("logzio-api-url", "https://api.logz.io", "LOGZIO API URL")
	}
	if flag.Lookup("rules-ds") == nil {
		rulesDSFlag = flag.String("rules-ds", "", "name of the data source for the alert rules")
	}
	if flag.Lookup("env-id") == nil {
		envIDFlag = flag.String("env-id", "my-env", "environment identifier, usually cluster name")
	}
	if flag.Lookup("workers") == nil {
		workerCountFlag = flag.Int("workers", 2, "The number of workers to process the alerts")
	}
	if flag.Lookup("ignore-slack-text") == nil {
		ignoreSlackTextFlag = flag.Bool("ignore-slack-text", false, "Ignore slack contact points text field")
	}
	if flag.Lookup("ignore-slack-title") == nil {
		ignoreSlackTitleFlag = flag.Bool("ignore-slack-title", false, "Ignore slack contact points title field")
	}
	// Parse the flags
	flag.Parse()

	if *helpFlag {
		flag.PrintDefaults()
		os.Exit(0)
	}

	// Environment variables have higher precedence than flags
	logzioAPIURL := getEnvWithFallback("LOGZIO_API_URL", *logzioAPIURLFlag)
	envID := getEnvWithFallback("ENV_ID", *envIDFlag)

	ignoreSlackText := getEnvWithFallback("IGNORE_SLACK_TEXT", strconv.FormatBool(*ignoreSlackTextFlag))
	ignoreSlackTextBool, err := strconv.ParseBool(ignoreSlackText)
	if err != nil {
		klog.Fatal("Invalid value for IGNORE_SLACK_TEXT: ", err.Error())
	}

	ignoreSlackTitle := getEnvWithFallback("IGNORE_SLACK_TITLE", strconv.FormatBool(*ignoreSlackTitleFlag))
	ignoreSlackTitleBool, err := strconv.ParseBool(ignoreSlackTitle)
	if err != nil {
		klog.Fatal("Invalid value for IGNORE_SLACK_TITLE", err.Error())
	}

	// api token is mandatory
	logzioAPIToken := getEnvWithFallback("LOGZIO_API_TOKEN", *logzioAPITokenFlag)
	if logzioAPIToken == "" {
		klog.Fatal("No logzio api token provided")
	}
	rulesDS := getEnvWithFallback("RULES_DS", *rulesDSFlag)
	if rulesDS == "" {
		klog.Fatal("No rules data source provided")
	}
	// Annotation must be provided either by flag or environment variable
	rulesAnnotation := getEnvWithFallback("RULES_CONFIGMAP_ANNOTATION", *rulesConfigmapAnnotation)
	if rulesAnnotation == "" {
		klog.Fatal("No rules configmap annotation provided")
	}
	// Annotation must be provided either by flag or environment variable
	alertManagerAnnotation := getEnvWithFallback("ALERTMANAGER_CONFIGMAP_ANNOTATION", *alertManagerConfigmapAnnotation)
	if alertManagerAnnotation == "" {
		klog.Fatal("No alert manager configmap annotation provided")
	}
	workerCountStr := getEnvWithFallback("WORKERS_COUNT", strconv.Itoa(*workerCountFlag))
	workerCount, err := strconv.Atoi(workerCountStr)

	if err != nil {
		workerCount = 2 // default value
	}

	return &Config{
		RulesAnnotation:        rulesAnnotation,
		AlertManagerAnnotation: alertManagerAnnotation,
		LogzioAPIToken:         logzioAPIToken,
		LogzioAPIURL:           logzioAPIURL,
		RulesDS:                rulesDS,
		EnvID:                  envID,
		WorkerCount:            workerCount,
		IgnoreSlackText:        ignoreSlackTextBool,
		IgnoreSlackTitle:       ignoreSlackTitleBool,
	}
}

// getEnvWithFallback tries to get the value from an environment variable and falls back to the given default value if not found.
func getEnvWithFallback(envName, defaultValue string) string {
	if value, exists := os.LookupEnv(envName); exists {
		return value
	}
	return defaultValue
}

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

func CreateNameStub(cm *corev1.ConfigMap) string {
	name := cm.GetObjectMeta().GetName()
	namespace := cm.GetObjectMeta().GetNamespace()

	return fmt.Sprintf("%s-%s", namespace, name)
}

// IsAlertEqual compares two AlertRule objects for equality.
func IsAlertEqual(rule rulefmt.RuleNode, grafanaRule grafanaalerts.GrafanaAlertRule) bool {
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
	if rule.For.String() != grafanaRule.For {
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

// Config holds all the configuration needed for the application to run.
type Config struct {
	RulesAnnotation        string
	AlertManagerAnnotation string
	LogzioAPIToken         string
	LogzioAPIURL           string
	RulesDS                string
	EnvID                  string
	WorkerCount            int
	IgnoreSlackText        bool
	IgnoreSlackTitle       bool
}
