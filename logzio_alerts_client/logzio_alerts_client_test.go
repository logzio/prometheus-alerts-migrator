package logzio_alerts_client

import (
	"github.com/logzio/prometheus-alerts-migrator/common"
	"github.com/prometheus/alertmanager/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func generateTestLogzioGrafanaAlertsClient() *LogzioGrafanaAlertsClient {
	ctlConfig := common.NewConfig()
	logzioGrafanaAlertsClient := NewLogzioGrafanaAlertsClient(ctlConfig.LogzioAPIToken, ctlConfig.LogzioAPIURL, ctlConfig.RulesDS, ctlConfig.EnvID, ctlConfig.IgnoreSlackTitle, ctlConfig.IgnoreSlackTitle)
	return logzioGrafanaAlertsClient

}

func TestGenerateGrafanaAlert(t *testing.T) {
	cl := generateTestLogzioGrafanaAlertsClient()
	// Define common rule parts for reuse in test cases
	baseRule := rulefmt.RuleNode{
		Alert:       yaml.Node{Value: "TestAlert"},
		Expr:        yaml.Node{Value: "up == 1"},
		For:         model.Duration(5 * time.Minute),
		Labels:      map[string]string{"severity": "critical"},
		Annotations: map[string]string{"description": "Instance is down"},
	}
	invalidRule := rulefmt.RuleNode{
		Alert:       yaml.Node{Value: "TestAlertInvalid"},
		Expr:        yaml.Node{Value: "up as== 1sadsa"},
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
		{
			name:      "invalid rule",
			rule:      invalidRule,
			folderUid: baseFolderUid,
			wantErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alertRule, err := cl.generateGrafanaAlert(tc.rule, tc.folderUid)

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

func TestGenerateGrafanaContactPoint(t *testing.T) {
	client := generateTestLogzioGrafanaAlertsClient()
	testCases := []struct {
		name           string
		receiver       config.Receiver
		expectedLength int
		expectedType   string
	}{
		{
			name: "Email Configuration",
			receiver: config.Receiver{
				EmailConfigs: []*config.EmailConfig{
					{
						To: "test@example.com",
					},
					{
						To: "test2@example.com",
					},
				},
			},
			expectedLength: 2,
			expectedType:   common.TypeEmail,
		},
		{
			name: "Slack Configuration",
			receiver: config.Receiver{
				SlackConfigs: []*config.SlackConfig{
					{
						Channel: "#test",
						APIURL: &config.SecretURL{
							URL: &url.URL{
								Scheme: "https",
								Host:   "api.slack.com",
								Path:   "/api/chat.postMessage",
							},
						},
					},
				},
			},
			expectedLength: 1,
			expectedType:   common.TypeSlack,
		},
		{
			name: "Pagerduty Configuration",
			receiver: config.Receiver{
				PagerdutyConfigs: []*config.PagerdutyConfig{
					{
						ServiceKey: "test",
					},
				},
			},
			expectedLength: 1,
			expectedType:   common.TypePagerDuty,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			contactPoints := client.generateGrafanaContactPoint(tc.receiver)
			assert.Len(t, contactPoints, tc.expectedLength, "Incorrect number of contact points generated")
			// Assert the type of contact point
			if tc.expectedLength > 0 {
				assert.Equal(t, tc.expectedType, contactPoints[0].Type, "Incorrect type of contact point")
				// Add more assertions to check other fields like settings, name, etc.
			}
		})
	}
}
