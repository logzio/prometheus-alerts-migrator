package logzio_alerts_client

import (
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/rulefmt"
	"gopkg.in/yaml.v3"
	"os"
	"reflect"
	"testing"
	"time"
)

func generateTestLogzioGrafanaAlertsClient() *LogzioGrafanaAlertsClient {
	logzioUrl := os.Getenv("LOGZIO_API_URL")
	logzioAPIToken := os.Getenv("LOGZIO_API_TOKEN")
	rulesDS := os.Getenv("RULES_DS")
	logzioGrafanaAlertsClient := NewLogzioGrafanaAlertsClient(logzioUrl, logzioAPIToken, rulesDS, "integration-test")
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
