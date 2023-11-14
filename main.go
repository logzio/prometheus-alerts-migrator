package main

import (
	"flag"
	"os"
	"strconv"
	"time"

	"github.com/logzio/prometheus-alerts-migrator/controller"
	"github.com/logzio/prometheus-alerts-migrator/pkg/signals"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
)

// Config holds all the configuration needed for the application to run.
type Config struct {
	Annotation     string
	LogzioAPIToken string
	LogzioAPIURL   string
	RulesDS        string
	EnvID          string
	WorkerCount    int
}

// NewConfig creates a Config struct, populating it with values from command-line flags and environment variables.
func NewConfig() *Config {
	// Define flags
	helpFlag := flag.Bool("help", false, "Display help")
	configmapAnnotation := flag.String("annotation", "prometheus.io/kube-rules", "Annotation that states that this configmap contains prometheus rules")
	logzioAPITokenFlag := flag.String("logzio-api-token", "", "LOGZIO API token")
	logzioAPIURLFlag := flag.String("logzio-api-url", "https://api.logz.io", "LOGZIO API URL")
	rulesDSFlag := flag.String("rules-ds", "", "name of the data source for the alert rules")
	envIDFlag := flag.String("env-id", "my-env", "environment identifier, usually cluster name")
	workerCountFlag := flag.Int("workers", 2, "number of workers to use for the controller")

	// Parse the flags
	flag.Parse()

	if *helpFlag {
		flag.PrintDefaults()
		os.Exit(0)
	}

	// Environment variables have lower precedence than flags
	logzioAPIURL := getEnvWithFallback("LOGZIO_API_URL", *logzioAPIURLFlag)
	envID := getEnvWithFallback("ENV_ID", *envIDFlag)
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
	annotation := getEnvWithFallback("CONFIGMAP_ANNOTATION", *configmapAnnotation)
	if annotation == "" {
		klog.Fatal("No ConfigMap annotation provided")
	}
	workerCountStr := getEnvWithFallback("WORKERS_COOUNT", strconv.Itoa(*workerCountFlag))
	workerCount, err := strconv.Atoi(workerCountStr)
	if err != nil {
		workerCount = 2 // default value
	}

	return &Config{
		Annotation:     annotation,
		LogzioAPIToken: logzioAPIToken,
		LogzioAPIURL:   logzioAPIURL,
		RulesDS:        rulesDS,
		EnvID:          envID,
		WorkerCount:    workerCount,
	}
}

// getEnvWithFallback tries to get the value from an environment variable and falls back to the given default value if not found.
func getEnvWithFallback(envName, defaultValue string) string {
	if value, exists := os.LookupEnv(envName); exists {
		return value
	}
	return defaultValue
}

func main() {
	config := NewConfig()

	klog.Info("Rule Updater starting.\n")
	klog.Infof("ConfigMap annotation: %s\n", config.Annotation)
	klog.Infof("Environment ID: %s\n", config.EnvID)
	klog.Infof("Logzio api url: %s\n", config.LogzioAPIURL)
	klog.Infof("Logzio rules data source: %s\n", config.RulesDS)
	klog.Infof("Number of workers: %d\n", config.WorkerCount)

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := controller.GetConfig()
	if err != nil {
		klog.Fatalf("Error getting Kubernetes config: %s", err)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err)
	}

	kubeInformerFactory := informers.NewSharedInformerFactory(kubeClient, time.Second*30)

	c := controller.NewController(kubeClient, kubeInformerFactory.Core().V1().ConfigMaps(), &config.Annotation, config.LogzioAPIToken, config.LogzioAPIURL, config.RulesDS, config.EnvID)

	kubeInformerFactory.Start(stopCh)

	if err = c.Run(config.WorkerCount, stopCh); err != nil {
		klog.Fatalf("Error running controller: %s", err)
	}
}
