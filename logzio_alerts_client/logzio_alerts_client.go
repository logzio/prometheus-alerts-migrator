package logzio_alerts_client

import (
	"encoding/json"
	"fmt"
	"github.com/logzio/logzio_terraform_client/grafana_alerts"
	"github.com/logzio/logzio_terraform_client/grafana_contact_points"
	"github.com/logzio/logzio_terraform_client/grafana_datasources"
	"github.com/logzio/logzio_terraform_client/grafana_folders"
	"github.com/logzio/logzio_terraform_client/grafana_notification_policies"
	"github.com/logzio/prometheus-alerts-migrator/common"
	alert_manager_config "github.com/prometheus/alertmanager/config"
	"github.com/prometheus/prometheus/model/rulefmt"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	"regexp"
	"strings"
)

const (
	refIdA                 = "A"
	refIdB                 = "B"
	expressionString       = "__expr__"
	queryType              = "query"
	alertFolder            = "prometheus-alerts"
	randomStringLength     = 5
	grafanaDefaultReceiver = "default-email"
)

// ReduceQueryModel represents a reduce query for time series data
type ReduceQueryModel struct {
	DataSource map[string]string `json:"datasource"`
	Expression string            `json:"expression"`
	Hide       bool              `json:"hide"`
	RefId      string            `json:"refId"`
	Reducer    string            `json:"reducer"`
	Type       string            `json:"type"`
}

// ToJSON marshals the Query model into a JSON byte slice
func (r ReduceQueryModel) ToJSON() (json.RawMessage, error) {
	marshaled, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return marshaled, nil
}

// PrometheusQueryModel represents a Prometheus query.
type PrometheusQueryModel struct {
	Expr  string `json:"expr"`
	Hide  bool   `json:"hide"`
	RefId string `json:"refId"`
}

// ToJSON marshals the Query into a JSON byte slice
func (p PrometheusQueryModel) ToJSON() (json.RawMessage, error) {
	marshaled, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return marshaled, nil
}

type LogzioGrafanaAlertsClient struct {
	AlertManagerGlobalConfig       *alert_manager_config.GlobalConfig
	logzioAlertClient              *grafana_alerts.GrafanaAlertClient
	logzioFolderClient             *grafana_folders.GrafanaFolderClient
	logzioDataSourceClient         *grafana_datasources.GrafanaDatasourceClient
	logzioContactPointClient       *grafana_contact_points.GrafanaContactPointClient
	logzioNotificationPolicyClient *grafana_notification_policies.GrafanaNotificationPolicyClient
	rulesDataSource                string
	envId                          string
	ignoreSlackText                bool
	ignoreSlackTitle               bool
}

func NewLogzioGrafanaAlertsClient(logzioApiToken string, logzioApiUrl string, rulesDs string, envId string, ignoreSlackText bool, ignoreSlackTitle bool) *LogzioGrafanaAlertsClient {
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
	logzioDataSourceClient, err := grafana_datasources.New(logzioApiToken, logzioApiUrl)
	if err != nil {
		klog.Errorf("Failed to create logzio datasource client: %v", err)
		return nil
	}
	logzioContactPointClient, err := grafana_contact_points.New(logzioApiToken, logzioApiUrl)
	if err != nil {
		klog.Errorf("Failed to create logzio contact point client: %v", err)
		return nil
	}
	logzioNotificationPolicyClient, err := grafana_notification_policies.New(logzioApiToken, logzioApiUrl)
	if err != nil {
		klog.Errorf("Failed to create logzio notification policy client: %v", err)
		return nil
	}
	// get datasource uid and validate value and type
	rulesDsData, err := logzioDataSourceClient.GetForAccount(rulesDs)
	if err != nil || rulesDsData.Uid == "" {
		klog.Errorf("Failed to get datasource uid: %v", err)
		return nil
	}
	if rulesDsData.Type != "prometheus" {
		klog.Errorf("Datasource type is not prometheus: %v", err)
		return nil
	}
	return &LogzioGrafanaAlertsClient{
		logzioAlertClient:              logzioAlertClient,
		logzioFolderClient:             logzioFolderClient,
		logzioDataSourceClient:         logzioDataSourceClient,
		logzioContactPointClient:       logzioContactPointClient,
		logzioNotificationPolicyClient: logzioNotificationPolicyClient,
		rulesDataSource:                rulesDsData.Uid,
		envId:                          envId,
		ignoreSlackText:                ignoreSlackText,
		ignoreSlackTitle:               ignoreSlackTitle,
	}
}

func (l *LogzioGrafanaAlertsClient) ResetNotificationPolicyTree() error {
	defaultGrafanaNotificationPolicy := grafana_notification_policies.GrafanaNotificationPolicyTree{
		Receiver: grafanaDefaultReceiver,
		Routes:   []grafana_notification_policies.GrafanaNotificationPolicy{},
	}
	err := l.logzioNotificationPolicyClient.SetupGrafanaNotificationPolicyTree(defaultGrafanaNotificationPolicy)
	if err != nil {
		return err
	}
	return nil
}

// SetNotificationPolicyTreeFromRouteTree converts route tree to grafana notification policy tree and writes it to logz.io
func (l *LogzioGrafanaAlertsClient) SetNotificationPolicyTreeFromRouteTree(routeTree *alert_manager_config.Route) {
	// getting logzio contact points to ensure it exists for the notification policy tree
	logzioContactPoints, err := l.GetLogzioManagedGrafanaContactPoints()
	if err != nil {
		klog.Errorf("Failed to get logz.io managed contact points: %v", err)
		return
	}
	// create contact points map for efficient lookup
	existingContactPoints := make(map[string]bool)
	for _, contactPoint := range logzioContactPoints {
		existingContactPoints[contactPoint.Name] = true
	}
	notificationPolicyTree := l.convertRouteTreeToNotificationPolicyTree(routeTree, existingContactPoints)
	err = l.logzioNotificationPolicyClient.SetupGrafanaNotificationPolicyTree(notificationPolicyTree)
	if err != nil {
		klog.Errorf("Failed to create notification policy tree: %v", err)
	}
}

func (l *LogzioGrafanaAlertsClient) convertRouteTreeToNotificationPolicyTree(routeTree *alert_manager_config.Route, existingContactPoints map[string]bool) (notificationPolicyTree grafana_notification_policies.GrafanaNotificationPolicyTree) {
	// checking for empty values to avoid nil pointer errors
	if routeTree.GroupByStr != nil {
		notificationPolicyTree.GroupBy = routeTree.GroupByStr
	}
	if routeTree.GroupInterval != nil {
		notificationPolicyTree.GroupInterval = routeTree.GroupInterval.String()
	}
	if routeTree.GroupWait != nil {
		notificationPolicyTree.GroupWait = routeTree.GroupWait.String()
	}
	if routeTree.RepeatInterval != nil {
		notificationPolicyTree.RepeatInterval = routeTree.RepeatInterval.String()
	}
	notificationPolicyTree.Receiver = routeTree.Receiver
	for _, childRoute := range routeTree.Routes {
		// check if the receiver of the child route exists in `existingContactPoints`
		if _, ok := existingContactPoints[childRoute.Receiver]; ok {
			notificationPolicy := l.generateGrafanaNotificationPolicy(childRoute)
			notificationPolicyTree.Routes = append(notificationPolicyTree.Routes, notificationPolicy)
		}
	}
	return notificationPolicyTree
}

// generateGrafanaNotificationPolicy generates a GrafanaNotificationPolicy from a alert_manager_config.Route
func (l *LogzioGrafanaAlertsClient) generateGrafanaNotificationPolicy(route *alert_manager_config.Route) (notificationPolicy grafana_notification_policies.GrafanaNotificationPolicy) {
	// checking for empty values to avoid nil pointer errors
	if route.GroupInterval != nil {
		notificationPolicy.GroupInterval = route.GroupInterval.String()
	}
	if route.GroupWait != nil {
		notificationPolicy.GroupWait = route.GroupWait.String()
	}
	if route.RepeatInterval != nil {
		notificationPolicy.RepeatInterval = route.RepeatInterval.String()
	}
	if route.GroupByStr != nil {
		notificationPolicy.GroupBy = route.GroupByStr
	}
	notificationPolicy.Receiver = route.Receiver
	routeMatchersYaml, err := route.Matchers.MarshalYAML()
	if err != nil {
		utilruntime.HandleError(err)
		return grafana_notification_policies.GrafanaNotificationPolicy{}
	}
	// converting the route matchers to the Grafana format
	routeMatchersList := routeMatchersYaml.([]string)
	grafanaObjMatchers := grafana_notification_policies.MatchersObj{}
	for _, routeMatcher := range routeMatchersList {
		// we split the route matcher by the regex (=|~|=|!=) to convert it to the Grafana format
		regex := regexp.MustCompile(`(=|~=?|!=)`)
		parts := regex.FindStringSubmatchIndex(routeMatcher)
		if len(parts) > 0 {
			// Extracting the key, operator, and value
			key := routeMatcher[:parts[0]]
			operator := routeMatcher[parts[0]:parts[1]]
			value := routeMatcher[parts[1]:]
			grafanaObjMatchers = append(grafanaObjMatchers, grafana_notification_policies.MatcherObj{key, operator, value})
		}
	}
	// handling `match` operators although it's deprecated to support users with old prometheus versions
	for key, value := range route.Match {
		grafanaObjMatchers = append(grafanaObjMatchers, grafana_notification_policies.MatcherObj{key, "=", value})
	}
	notificationPolicy.ObjectMatchers = grafanaObjMatchers
	// repeat the process for nested policies
	for _, childRoute := range route.Routes {
		childNotificationPolicy := l.generateGrafanaNotificationPolicy(childRoute)
		notificationPolicy.Routes = append(notificationPolicy.Routes, childNotificationPolicy)
	}
	return notificationPolicy
}

// WriteContactPoints writes the contact points to logz.io
func (l *LogzioGrafanaAlertsClient) WriteContactPoints(contactPointsToWrite []alert_manager_config.Receiver) {
	for _, contactPoint := range contactPointsToWrite {
		contactPointsList := l.generateGrafanaContactPoint(contactPoint)
		for _, cp := range contactPointsList {
			_, err := l.logzioContactPointClient.CreateGrafanaContactPoint(cp)
			if err != nil {
				klog.Warningf("Failed to create contact point: %v", err)
			}
		}
	}
}

// DeleteContactPoints deletes the contact points from logz.io
func (l *LogzioGrafanaAlertsClient) DeleteContactPoints(contactPointsToDelete []grafana_contact_points.GrafanaContactPoint) {
	for _, contactPoint := range contactPointsToDelete {
		err := l.logzioContactPointClient.DeleteGrafanaContactPoint(contactPoint.Uid)
		if err != nil {
			klog.Warningf("Failed to delete contact point: %v", err)
		}
	}
}

// UpdateContactPoints updates the contact points in logz.io
func (l *LogzioGrafanaAlertsClient) UpdateContactPoints(contactPointsToUpdate []alert_manager_config.Receiver, contactPointsMap []grafana_contact_points.GrafanaContactPoint) {
	for _, contactPoint := range contactPointsToUpdate {
		contactPointsList := l.generateGrafanaContactPoint(contactPoint)
		for _, cp := range contactPointsList {
			for _, logzioContactPoint := range contactPointsMap {
				if logzioContactPoint.Name == cp.Name {
					cp.Uid = logzioContactPoint.Uid
					err := l.logzioContactPointClient.UpdateContactPoint(cp)
					if err != nil {
						klog.Warningf("Failed to update contact point: %v", err)
					}
				}
			}
		}
	}
}

// generateGrafanaContactPoint generates a GrafanaContactPoint from a alert_manager_config.Receiver
func (l *LogzioGrafanaAlertsClient) generateGrafanaContactPoint(receiver alert_manager_config.Receiver) (contactPointsList []grafana_contact_points.GrafanaContactPoint) {
	// check for email type configs
	for _, emailConfig := range receiver.EmailConfigs {
		contactPoint := grafana_contact_points.GrafanaContactPoint{
			Name:                  receiver.Name,
			Type:                  common.TypeEmail,
			Uid:                   common.GenerateRandomString(9),
			DisableResolveMessage: false,
			Settings: map[string]interface{}{
				"addresses":   emailConfig.To,
				"message":     emailConfig.HTML,
				"singleEmail": true,
			},
		}
		contactPointsList = append(contactPointsList, contactPoint)
	}
	// check for slack type configs
	for _, slackConfig := range receiver.SlackConfigs {
		var url string
		if slackConfig.APIURL.String() != "" {
			url = slackConfig.APIURL.String()
		} else {
			url = l.AlertManagerGlobalConfig.SlackAPIURL.String()
		}
		contactPoint := grafana_contact_points.GrafanaContactPoint{
			Name:                  receiver.Name,
			Type:                  common.TypeSlack,
			Uid:                   common.GenerateRandomString(9),
			DisableResolveMessage: false,
			Settings: map[string]interface{}{
				"url":       url,
				"recipient": slackConfig.Channel,
				"username":  slackConfig.Username,
			},
		}
		// Adding title and text fields based on program flags
		if !l.ignoreSlackTitle {
			contactPoint.Settings["title"] = slackConfig.Title
		}
		if !l.ignoreSlackText {
			contactPoint.Settings["text"] = slackConfig.Text
		}
		contactPointsList = append(contactPointsList, contactPoint)
	}
	// check for pagerduty type configs
	for _, pagerdutyConfig := range receiver.PagerdutyConfigs {
		contactPoint := grafana_contact_points.GrafanaContactPoint{
			Name:                  receiver.Name,
			Type:                  common.TypePagerDuty,
			Uid:                   common.GenerateRandomString(9),
			DisableResolveMessage: false,
			Settings: map[string]interface{}{
				"integrationKey": pagerdutyConfig.ServiceKey,
				"description":    pagerdutyConfig.Description,
				"client":         pagerdutyConfig.Client,
				"clientUrl":      pagerdutyConfig.ClientURL,
			},
		}
		contactPointsList = append(contactPointsList, contactPoint)
	}
	return contactPointsList
}

// DeleteRules deletes the rules from logz.io
func (l *LogzioGrafanaAlertsClient) DeleteRules(rulesToDelete []grafana_alerts.GrafanaAlertRule, folderUid string) {
	for _, rule := range rulesToDelete {
		err := l.logzioAlertClient.DeleteGrafanaAlertRule(rule.Uid)
		if err != nil {
			klog.Warningf("Error deleting rule: %s - %s", rule.Title, err.Error())
		}
	}
}

// UpdateRules updates the rules in logz.io
func (l *LogzioGrafanaAlertsClient) UpdateRules(rulesToUpdate []rulefmt.RuleNode, logzioRulesMap map[string]grafana_alerts.GrafanaAlertRule, folderUid string) {
	for _, rule := range rulesToUpdate {
		// Retrieve the existing GrafanaAlertRule to get the Uid.
		existingRule := logzioRulesMap[rule.Alert.Value]
		alert, err := l.generateGrafanaAlert(rule, folderUid)
		if err != nil {
			klog.Warning(err)
			continue // Skip this rule and continue with the next
		}
		// Set the Uid from the existing rule.
		alert.Uid = existingRule.Uid
		err = l.logzioAlertClient.UpdateGrafanaAlertRule(alert)
		if err != nil {
			klog.Warningf("Error updating rule: %s - %s", alert.Title, err.Error())
		}
	}
}

// WriteRules writes the rules to logz.io
func (l *LogzioGrafanaAlertsClient) WriteRules(rulesToWrite []rulefmt.RuleNode, folderUid string) {
	for _, rule := range rulesToWrite {
		alert, err := l.generateGrafanaAlert(rule, folderUid)
		if err != nil {
			klog.Warning(err)
		}
		_, err = l.logzioAlertClient.CreateGrafanaAlertRule(alert)
		if err != nil {
			klog.Warning("Error writing rule:", alert.Title, err.Error())
		}
	}
}

// generateGrafanaAlert generates a GrafanaAlertRule from a Prometheus rule
func (l *LogzioGrafanaAlertsClient) generateGrafanaAlert(rule rulefmt.RuleNode, folderUid string) (grafana_alerts.GrafanaAlertRule, error) {
	// validate the rule
	validationErrs := rule.Validate()
	if len(validationErrs) > 0 {
		return grafana_alerts.GrafanaAlertRule{}, fmt.Errorf("invalid rule: %v", validationErrs)
	}
	// Create promql query to return time series data for the expression.
	promqlQuery := PrometheusQueryModel{
		Expr:  rule.Expr.Value,
		Hide:  false,
		RefId: refIdA,
	}
	// Use the ToJSON method to marshal the Query struct.
	promqlModel, err := promqlQuery.ToJSON()
	if err != nil {
		return grafana_alerts.GrafanaAlertRule{}, err
	}
	queryA := grafana_alerts.GrafanaAlertQuery{
		DatasourceUid: l.rulesDataSource,
		Model:         promqlModel,
		RefId:         refIdA,
		QueryType:     queryType,
		RelativeTimeRange: grafana_alerts.RelativeTimeRangeObj{
			From: 300,
			To:   0,
		},
	}
	// Create reduce query to return the reduced last value of the time series data.
	reduceQuery := ReduceQueryModel{
		DataSource: map[string]string{
			"type": expressionString,
			"uid":  expressionString,
		},
		Expression: refIdA,
		Hide:       false,
		RefId:      refIdB,
		Reducer:    "last",
		Type:       "reduce",
	}
	reduceModel, err := reduceQuery.ToJSON()
	if err != nil {
		return grafana_alerts.GrafanaAlertRule{}, err
	}
	queryB := grafana_alerts.GrafanaAlertQuery{
		DatasourceUid: expressionString,
		Model:         reduceModel,
		RefId:         refIdB,
		QueryType:     "",
		RelativeTimeRange: grafana_alerts.RelativeTimeRangeObj{
			From: 300,
			To:   0,
		},
	}
	duration, err := common.ParseDuration(rule.For.String())
	if err != nil {
		return grafana_alerts.GrafanaAlertRule{}, err
	}

	// Create the GrafanaAlertRule, we are alerting on the reduced last value of the time series data (query B).
	grafanaAlert := grafana_alerts.GrafanaAlertRule{
		Annotations:  rule.Annotations,
		Condition:    refIdB,
		Data:         []*grafana_alerts.GrafanaAlertQuery{&queryA, &queryB},
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

func (l *LogzioGrafanaAlertsClient) GetLogzioManagedGrafanaContactPoints() ([]grafana_contact_points.GrafanaContactPoint, error) {
	contactPoints, err := l.logzioContactPointClient.GetAllGrafanaContactPoints()
	if err != nil {
		return nil, err
	}
	var managedContactPoints []grafana_contact_points.GrafanaContactPoint
	for _, contactPoint := range contactPoints {
		// check if the contact point name contains the env id to determine if it is a managed contact point
		if strings.Contains(contactPoint.Name, l.envId) {
			managedContactPoints = append(managedContactPoints, contactPoint)
		}
	}
	return managedContactPoints, nil
}

func (l *LogzioGrafanaAlertsClient) GetLogzioGrafanaNotificationPolicies() (grafana_notification_policies.GrafanaNotificationPolicyTree, error) {
	notificationPolicies, err := l.logzioNotificationPolicyClient.GetGrafanaNotificationPolicyTree()
	if err != nil {
		return grafana_notification_policies.GrafanaNotificationPolicyTree{}, err
	}
	return notificationPolicies, nil

}

// GetLogzioGrafanaAlerts builds a list of rules from all logz.io
func (l *LogzioGrafanaAlertsClient) GetLogzioGrafanaAlerts(folderUid string) ([]grafana_alerts.GrafanaAlertRule, error) {
	alertRules, ListLogzioRulesErr := l.logzioAlertClient.ListGrafanaAlertRules()
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
	return alertsInFolder, nil
}

// FindOrCreatePrometheusAlertsFolder tries to find the prometheus alerts folder in logz.io, if it does not exist it creates it.
func (l *LogzioGrafanaAlertsClient) FindOrCreatePrometheusAlertsFolder() (string, error) {
	folders, err := l.logzioFolderClient.ListGrafanaFolders()
	if err != nil {
		return "", err
	}
	envFolderTitle := fmt.Sprintf("%s-%s", l.envId, alertFolder)
	// try to find the prometheus alerts folder
	for _, folder := range folders {
		if folder.Title == envFolderTitle {
			return folder.Uid, nil
		}
	}
	// if not found, create the prometheus alerts folder
	grafanaFolder, err := l.logzioFolderClient.CreateGrafanaFolder(grafana_folders.CreateUpdateFolder{
		Uid:   fmt.Sprintf("%s-%s", envFolderTitle, common.GenerateRandomString(randomStringLength)),
		Title: envFolderTitle,
	})
	if err != nil {
		return "", err
	}
	return grafanaFolder.Uid, nil
}
