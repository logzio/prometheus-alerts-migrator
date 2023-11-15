package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/logzio/logzio_terraform_client/grafana_alerts"
	"github.com/logzio/logzio_terraform_client/grafana_folders"
	"github.com/prometheus/prometheus/model/rulefmt"
	_ "github.com/prometheus/prometheus/promql/parser"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	corev1informers "k8s.io/client-go/informers/core/v1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

const (
	alertFolder         = "prometheus-alerts"
	controllerAgentName = "logzio-prometheus-alerts-migrator-controller"
	ErrInvalidKey       = "InvalidKey"
	letterBytes         = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterIdxBits       = 6                    // 6 bits to represent a letter index
	letterIdxMask       = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax        = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
	randomStringLength  = 5
	refId               = "A"
	queryType           = "query"
)

// PrometheusQuery represents a Prometheus query.
type PrometheusQuery struct {
	Expr  string `json:"expr"`
	Hide  bool   `json:"hide"`
	RefId string `json:"refId"`
}

// ToJSON marshals the Query into a JSON byte slice
func (p PrometheusQuery) ToJSON() (json.RawMessage, error) {
	marshaled, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return marshaled, nil
}

// Controller is the controller implementation for prometheus alert rules to logzio alert rules
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface

	configmapsLister corev1listers.ConfigMapLister
	configmapsSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder

	logzioAlertClient  *grafana_alerts.GrafanaAlertClient
	logzioFolderClient *grafana_folders.GrafanaFolderClient
	rulesDataSource    string
	envId              string

	resourceVersionMap         map[string]string
	interestingAnnotation      *string
	configmapEventRecorderFunc func(cm *corev1.ConfigMap, eventtype, reason, msg string)
}

type MultiRuleGroups struct {
	Values []rulefmt.RuleGroups
}

func NewController(
	kubeclientset *kubernetes.Clientset,
	configmapInformer corev1informers.ConfigMapInformer,
	interestingAnnotation *string,
	logzioApiToken string,
	logzioApiUrl string,
	rulesDs string,
	envId string,
) *Controller {

	utilruntime.Must(scheme.AddToScheme(scheme.Scheme))
	klog.Infof("Setting up event handlers")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	logzioAlertClient, err := grafana_alerts.New(logzioApiToken, logzioApiUrl)
	if err != nil {
		klog.Errorf("Failed to create logzio alert client: %v", err)
		return nil
	}
	logzioFolderClient, err := grafana_folders.New(logzioApiToken, logzioApiUrl)
	if err != nil {
		klog.Errorf("Failed to create logzio folder client: %v", err)
		return nil
	}
	controller := &Controller{
		kubeclientset:         kubeclientset,
		configmapsLister:      configmapInformer.Lister(),
		configmapsSynced:      configmapInformer.Informer().HasSynced,
		workqueue:             workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		recorder:              recorder,
		interestingAnnotation: interestingAnnotation,
		resourceVersionMap:    make(map[string]string),
		logzioAlertClient:     logzioAlertClient,
		logzioFolderClient:    logzioFolderClient,
		rulesDataSource:       rulesDs,
		envId:                 envId,
	}

	controller.configmapEventRecorderFunc = controller.recordEventOnConfigMap
	klog.Info("Setting up event handlers")
	configmapInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueConfigMap,
		UpdateFunc: func(old, new interface{}) {
			newCM := new.(*corev1.ConfigMap)
			oldCM := old.(*corev1.ConfigMap)
			if newCM.ResourceVersion == oldCM.ResourceVersion {
				return
			}
			controller.enqueueConfigMap(newCM)
		},
		DeleteFunc: controller.enqueueConfigMap,
	})

	return controller
}

func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Infof("Starting %s", controllerAgentName)

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.configmapsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Foo resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two
func (c *Controller) syncHandler(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	configmap, err := c.getConfigMap(namespace, name)
	if err != nil {
		return err
	}

	bypassCheck := false
	if configmap == nil {
		// deleted
		bypassCheck = true
	}

	if c.isRuleConfigMap(configmap) || bypassCheck {
		cmList, err := c.kubeclientset.CoreV1().ConfigMaps(corev1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("unable to collect configmaps from the cluster; %s", err))
			return nil
		}

		if c.haveConfigMapsChanged(cmList) {
			return c.processConfigMapsChanges(cmList)
		}
	}

	return nil
}

// getConfigMap returns the ConfigMap with the specified name in the specified namespace, or nil if no such ConfigMap exists.
func (c *Controller) getConfigMap(namespace, name string) (*corev1.ConfigMap, error) {
	configmap, err := c.configmapsLister.ConfigMaps(namespace).Get(name)
	if errors.IsNotFound(err) {
		utilruntime.HandleError(fmt.Errorf("configmap '%s' in work queue no longer exists", name))
		return nil, nil
	}
	return configmap, err
}

// processConfigMapsChanges gets the state of alert rules from both cluster configmaps and logz.io, compares the rules and decide what crud operations to perform
func (c *Controller) processConfigMapsChanges(mapList *corev1.ConfigMapList) error {
	alertRules := c.getClusterAlertRules(mapList)
	folderUid, err := c.findOrCreatePrometheusAlertsFolder()
	if err != nil {
		utilruntime.HandleError(err)
		return err
	}

	logzioAlertRules, err := c.getLogzioGrafanaAlerts(folderUid)
	if err != nil {
		utilruntime.HandleError(err)
		return err
	}
	// Maps for efficient lookups by alert name.
	rulesMap := make(map[string]rulefmt.RuleNode, len(*alertRules))
	logzioRulesMap := make(map[string]grafana_alerts.GrafanaAlertRule, len(*logzioAlertRules))
	// Process Kubernetes alerts into a map.
	for _, alert := range *alertRules {
		rulesMap[alert.Alert.Value] = alert
	}
	// Process Logz.io alerts into a map.
	for _, alert := range *logzioAlertRules {
		logzioRulesMap[alert.Title] = alert
	}
	toAdd, toUpdate, toDelete := c.compareAlertRules(rulesMap, logzioRulesMap)
	klog.Infof("Alert rules summary: to add: %d, to update: %d, to delete: %d", len(toAdd), len(toUpdate), len(toDelete))

	if len(toAdd) > 0 {
		c.writeRules(toAdd, folderUid)
	}
	if len(toUpdate) > 0 {
		c.updateRules(toUpdate, logzioRulesMap, folderUid)
	}
	if len(toDelete) > 0 {
		c.deleteRules(toDelete, folderUid)
	}

	return nil
}

// deleteRules deletes the rules from logz.io
func (c *Controller) deleteRules(rulesToDelete []grafana_alerts.GrafanaAlertRule, folderUid string) {
	for _, rule := range rulesToDelete {
		err := c.logzioAlertClient.DeleteGrafanaAlertRule(rule.Uid)
		if err != nil {
			klog.Warningf("Error deleting rule: %s - %s", rule.Title, err.Error())
		}
	}
}

// updateRules updates the rules in logz.io
func (c *Controller) updateRules(rulesToUpdate []rulefmt.RuleNode, logzioRulesMap map[string]grafana_alerts.GrafanaAlertRule, folderUid string) {
	for _, rule := range rulesToUpdate {
		// Retrieve the existing GrafanaAlertRule to get the Uid.
		existingRule := logzioRulesMap[rule.Alert.Value]
		alert, err := c.generateGrafanaAlert(rule, folderUid)
		if err != nil {
			klog.Warning(err)
			continue // Skip this rule and continue with the next
		}
		// Set the Uid from the existing rule.
		alert.Uid = existingRule.Uid
		err = c.logzioAlertClient.UpdateGrafanaAlertRule(alert)
		if err != nil {
			klog.Warningf("Error updating rule: %s - %s", alert.Title, err.Error())
		}
	}
}

// writeRules writes the rules to logz.io
func (c *Controller) writeRules(rulesToWrite []rulefmt.RuleNode, folderUid string) {
	for _, rule := range rulesToWrite {
		alert, err := c.generateGrafanaAlert(rule, folderUid)
		if err != nil {
			klog.Warning(err)
		}
		_, err = c.logzioAlertClient.CreateGrafanaAlertRule(alert)
		if err != nil {
			klog.Warning("Error writing rule:", alert.Title, err.Error())
		}
	}
}

// generateGrafanaAlert generates a GrafanaAlertRule from a Prometheus rule
func (c *Controller) generateGrafanaAlert(rule rulefmt.RuleNode, folderUid string) (grafana_alerts.GrafanaAlertRule, error) {
	// Create an instance of the Prometheus query.
	query := PrometheusQuery{
		Expr:  rule.Expr.Value,
		Hide:  false,
		RefId: refId,
	}
	// Use the ToJSON method to marshal the Query struct.
	model, err := query.ToJSON()
	if err != nil {
		return grafana_alerts.GrafanaAlertRule{}, err
	}
	data := grafana_alerts.GrafanaAlertQuery{
		DatasourceUid: c.rulesDataSource,
		Model:         model,
		RefId:         refId,
		QueryType:     queryType,
		RelativeTimeRange: grafana_alerts.RelativeTimeRangeObj{
			From: 300,
			To:   0,
		},
	}
	duration, err := parseDuration(rule.For.String())
	if err != nil {
		return grafana_alerts.GrafanaAlertRule{}, err
	}
	grafanaAlert := grafana_alerts.GrafanaAlertRule{
		Annotations:  rule.Annotations,
		Condition:    refId,
		Data:         []*grafana_alerts.GrafanaAlertQuery{&data},
		FolderUID:    folderUid,
		NoDataState:  grafana_alerts.NoDataOk,
		ExecErrState: grafana_alerts.ErrOK,
		Labels:       rule.Labels,
		OrgID:        1,
		RuleGroup:    rule.Alert.Value,
		Title:        rule.Alert.Value,
		For:          duration,
	}
	return grafanaAlert, nil
}

// enqueueConfigMap get the cm on the workqueue
func (c *Controller) enqueueConfigMap(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// getClusterAlertRules builds a list of rules from all the configmaps in the cluster
func (c *Controller) getClusterAlertRules(mapList *corev1.ConfigMapList) *[]rulefmt.RuleNode {
	var finalRules []rulefmt.RuleNode
	for _, cm := range mapList.Items {
		if c.isRuleConfigMap(&cm) {
			cmRules := c.extractValues(&cm)
			if len(cmRules) > 0 {
				finalRules = append(finalRules, cmRules...)

			}
		}
	}
	return &finalRules
}

// getLogzioGrafanaAlerts builds a list of rules from all logz.io
func (c *Controller) getLogzioGrafanaAlerts(folderUid string) (*[]grafana_alerts.GrafanaAlertRule, error) {
	alertRules, ListLogzioRulesErr := c.logzioAlertClient.ListGrafanaAlertRules()
	if ListLogzioRulesErr != nil {
		return nil, ListLogzioRulesErr
	}
	// find all alerts inside prometheus alerts folder
	var alertsInFolder []grafana_alerts.GrafanaAlertRule
	for _, rule := range alertRules {
		if rule.FolderUID == folderUid {
			alertsInFolder = append(alertsInFolder, rule)
		}
	}
	return &alertsInFolder, nil
}

// findOrCreatePrometheusAlertsFolder tries to find the prometheus alerts folder in logz.io, if it does not exist it creates it.
func (c *Controller) findOrCreatePrometheusAlertsFolder() (string, error) {
	folders, err := c.logzioFolderClient.ListGrafanaFolders()
	if err != nil {
		return "", err
	}
	envFolderTitle := fmt.Sprintf("%s-%s", c.envId, alertFolder)
	// try to find the prometheus alerts folder
	for _, folder := range folders {
		if folder.Title == envFolderTitle {
			return folder.Uid, nil
		}
	}
	// if not found, create the prometheus alerts folder
	grafanaFolder, err := c.logzioFolderClient.CreateGrafanaFolder(grafana_folders.CreateUpdateFolder{
		Uid:   fmt.Sprintf("%s-%s", envFolderTitle, generateRandomString(randomStringLength)),
		Title: envFolderTitle,
	})
	if err != nil {
		return "", err
	}
	return grafanaFolder.Uid, nil
}

// extractValues extracts the rules from the configmap, and validates them
func (c *Controller) extractValues(cm *corev1.ConfigMap) []rulefmt.RuleNode {

	fallbackNameStub := createNameStub(cm)

	var toalRules []rulefmt.RuleNode

	for key, value := range cm.Data {
		// try each encoding
		// try to extract a rules
		var rule rulefmt.RuleNode
		var err error
		err, rule = c.extractRules(value)
		if err != nil {
			errorMsg := fmt.Sprintf("Configmap: %s key: %s Error during extraction.", fallbackNameStub, key)
			c.configmapEventRecorderFunc(cm, corev1.EventTypeWarning, ErrInvalidKey, errorMsg)
		}

		// Add unique name for the alert rule to prevent duplicate rules ([alert_name]-[configmap_name]-[configmap_namespace])
		rule.Alert.Value = fmt.Sprintf("%s-%s-%s", cm.Name, cm.Namespace, key)

		if len(rule.Alert.Value) == 0 {
			errorMsg := fmt.Sprintf("Configmap: %s key: %s does not conform to any of the legal format Skipping.", fallbackNameStub, key)
			c.configmapEventRecorderFunc(cm, corev1.EventTypeWarning, ErrInvalidKey, errorMsg)
		} else {
			// validate the rule
			validationErrs := rule.Validate()
			if len(validationErrs) > 0 {
				for _, ruleErr := range validationErrs {
					c.configmapEventRecorderFunc(cm, corev1.EventTypeWarning, ErrInvalidKey, ruleErr.Error())
				}
				failMessage := fmt.Sprintf("Configmap: %s key: %s Rejected, no valid rules.", fallbackNameStub, key)
				c.configmapEventRecorderFunc(cm, corev1.EventTypeWarning, ErrInvalidKey, failMessage)

			} else {
				// add to the rulegroups
				toalRules = append(toalRules, rule)
			}
		}
	}
	klog.Info(fmt.Sprintf("Found %d rules in %s configmap", len(toalRules), cm.Name))

	return toalRules
}

// extractRules extracts the rules from the configmap key
func (c *Controller) extractRules(value string) (error, rulefmt.RuleNode) {
	rule := rulefmt.RuleNode{}
	err := yaml.Unmarshal([]byte(value), &rule)
	if err != nil {
		return err, rulefmt.RuleNode{}
	}
	if len(rule.Alert.Value) == 0 {
		return fmt.Errorf("no Rules found"), rule
	}
	return nil, rule
}

// compareAlertRules compares the rules from Kubernetes with those in Logz.io.
// It returns three slices of rulefmt.RuleNode and grafana_alerts.GrafanaAlertRule indicating which rules to add, update, or delete.
func (c *Controller) compareAlertRules(k8sRulesMap map[string]rulefmt.RuleNode, logzioRulesMap map[string]grafana_alerts.GrafanaAlertRule) (toAdd, toUpdate []rulefmt.RuleNode, toDelete []grafana_alerts.GrafanaAlertRule) {
	// Determine rules to add or update.
	for alertName, k8sRule := range k8sRulesMap {
		logzioRule, exists := logzioRulesMap[alertName]
		if !exists {
			// Alert doesn't exist in Logz.io, needs to be added.
			toAdd = append(toAdd, k8sRule)
		} else if !isAlertEqual(k8sRule, logzioRule) {
			// Alert exists but differs, needs to be updated.
			toUpdate = append(toUpdate, k8sRule)
		}
	}

	// Determine rules to delete.
	for alertName := range logzioRulesMap {
		if _, exists := k8sRulesMap[alertName]; !exists {
			// Alert is in Logz.io but not in Kubernetes, needs to be deleted.
			toDelete = append(toDelete, logzioRulesMap[alertName])
		}
	}

	return toAdd, toUpdate, toDelete
}

// isRuleConfigMap checks if the configmap is a rule configmap
func (c *Controller) isRuleConfigMap(cm *corev1.ConfigMap) bool {
	if cm == nil {
		return false
	}
	annotations := cm.GetObjectMeta().GetAnnotations()

	for key := range annotations {
		if key == *c.interestingAnnotation {
			return true
		}
	}

	return false
}

// haveConfigMapsChanged checks if the configmaps have changed
func (c *Controller) haveConfigMapsChanged(mapList *corev1.ConfigMapList) bool {
	changes := false
	if mapList == nil {
		klog.Warning("mapList is nil")
		return false
	}
	for _, cm := range mapList.Items {
		if c.isRuleConfigMap(&cm) {
			stub := createNameStub(&cm)
			val, ok := c.resourceVersionMap[stub]
			if !ok {
				// new configmap
				changes = true
			}
			if cm.ResourceVersion != val {
				// changed configmap
				changes = true
			}
			c.resourceVersionMap[stub] = cm.ResourceVersion
		}
	}

	return changes
}

// recordEventOnConfigMap records an event on the configmap
func (c *Controller) recordEventOnConfigMap(cm *corev1.ConfigMap, eventtype, reason, msg string) {
	c.recorder.Event(cm, eventtype, reason, msg)
	if eventtype == corev1.EventTypeWarning {
		klog.Warning(msg)
	}
}
