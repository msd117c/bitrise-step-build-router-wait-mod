package bitrise

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bitrise-io/go-utils/log"
	"github.com/hashicorp/go-retryablehttp"
)

// Pipeline ...
type Pipeline struct {
	Id                  string          `json:"id"`
	Name                string          `json:"name"`
	Status              string          `json:"status"`
	BuildNumber         int64           `json:"number_in_app_scope"`
	TriggerParams       json.RawMessage `json:"trigger_params"`
}

// IsRunning ...
func (pipeline Pipeline) IsRunning() bool {
	return pipeline.Status == "initializing" || pipeline.Status == "on_hold" || pipeline.Status == "running"
}

// IsSuccessful ...
func (pipeline Pipeline) IsSuccessful() bool {
	return build.Status == 1
}

// IsFailed ...
func (pipeline Pipeline) IsFailed() bool {
	return build.Status == 2
}

// IsAborted ...
func (pipeline Pipeline) IsAborted() bool {
	return build.Status == 3
}

// IsAbortedWithSuccess ...
func (pipeline Pipeline) IsAbortedWithSuccess() bool {
	return build.Status == 4
}

type pipelineResponse struct {
	Data Pipeline `json:"data"`
}

type pipelineAbortParams struct {
	AbortReason       string `json:"abort_reason"`
	AbortWithSucces   bool   `json:"abort_with_success"`
	SkipNotifications bool   `json:"skip_notifications"`
}

// Environment ...
type Environment struct {
	MappedTo string `json:"mapped_to"`
	Value    string `json:"value"`
}

// App ...
type App struct {
	BaseURL, Slug, AccessToken string
	IsDebugRetryTimings        bool
}

// NewAppWithDefaultURL returns a Bitrise client with the default URl
func NewAppWithDefaultURL(slug, accessToken string) App {
	return App{
		BaseURL:     "https://api.bitrise.io",
		Slug:        slug,
		AccessToken: accessToken,
	}
}

// RetryLogAdaptor adapts the retryablehttp.Logger interface to the go-utils logger.
type RetryLogAdaptor struct{}

// Printf implements the retryablehttp.Logger interface
func (*RetryLogAdaptor) Printf(fmtStr string, vars ...interface{}) {
	switch {
	case strings.HasPrefix(fmtStr, "[DEBUG]"):
		log.Debugf(strings.TrimSpace(fmtStr[7:]), vars...)
	case strings.HasPrefix(fmtStr, "[ERR]"):
		log.Errorf(strings.TrimSpace(fmtStr[5:]), vars...)
	case strings.HasPrefix(fmtStr, "[ERROR]"):
		log.Errorf(strings.TrimSpace(fmtStr[7:]), vars...)
	case strings.HasPrefix(fmtStr, "[WARN]"):
		log.Warnf(strings.TrimSpace(fmtStr[6:]), vars...)
	case strings.HasPrefix(fmtStr, "[INFO]"):
		log.Infof(strings.TrimSpace(fmtStr[6:]), vars...)
	default:
		log.Printf(fmtStr, vars...)
	}
}

// NewRetryableClient returns a retryable HTTP client
// isDebugRetryTimings sets the timeouts shoreter for testing purposes
func NewRetryableClient(isDebugRetryTimings bool) *retryablehttp.Client {
	client := retryablehttp.NewClient()
	client.CheckRetry = retryablehttp.DefaultRetryPolicy
	client.Backoff = retryablehttp.DefaultBackoff
	client.Logger = &RetryLogAdaptor{}
	client.ErrorHandler = retryablehttp.PassthroughErrorHandler
	if !isDebugRetryTimings {
		client.RetryWaitMin = 10 * time.Second
		client.RetryWaitMax = 60 * time.Second
		client.RetryMax = 5
	} else {
		client.RetryWaitMin = 100 * time.Millisecond
		client.RetryWaitMax = 400 * time.Millisecond
		client.RetryMax = 3
	}

	return client
}

// GetPipeline ...
func (app App) GetPipeline(pipelineId string) (pipeline Pipeline, err error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v0.1/apps/%s/pipelines/%s", app.BaseURL, app.Slug, pipelineId), nil)
	if err != nil {
		return Pipeline{}, err
	}

	req.Header.Add("Authorization", "token "+app.AccessToken)

	retryReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return Pipeline{}, fmt.Errorf("failed to create retryable request: %s", err)
	}

	client := NewRetryableClient(app.IsDebugRetryTimings)

	resp, err := client.Do(retryReq)
	if err != nil {
		return Pipeline{}, err
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return Pipeline{}, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Pipeline{}, fmt.Errorf("failed to get response, statuscode: %d, body: %s", resp.StatusCode, respBody)
	}

	var pipelineResponse pipelineResponse
	if err := json.Unmarshal(respBody, &pipelineResponse); err != nil {
		return Pipeline{}, fmt.Errorf("failed to decode response, body: %s, error: %s", respBody, err)
	}
	return pipelineResponse.Data, nil
}

// AbortPipeline ...
func (app App) AbortPipeline(pipelineId string, abortReason string) error {
	b, err := json.Marshal(pipelineAbortParams{
		AbortReason:       abortReason,
		AbortWithSucces:   false,
		SkipNotifications: true,
	})

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v0.1/apps/%s/pipelines/%s/abort", app.BaseURL, app.Slug, pipelineId), bytes.NewReader(b))
	if err != nil {
		return nil
	}
	req.Header.Add("Authorization", "token "+app.AccessToken)

	retryReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return fmt.Errorf("failed to create retryable request: %s", err)
	}

	retryClient := NewRetryableClient(app.IsDebugRetryTimings)

	resp, err := retryClient.Do(retryReq)
	if err != nil {
		return nil
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("failed to get response, statuscode: %d, body: %s", resp.StatusCode, respBody)
	}
	return nil
}

// WaitForPipelines ...
func (app App) WaitForPipelines(pipelineIds []string, statusChangeCallback func(build Build)) error {
	failed := false
	status := map[string]string{}
	for {
		running := 0
		for _, pipelineId := range pipelineIds {
			pipeline, err := app.GetPipeline(pipelineId)
			if err != nil {
				return fmt.Errorf("failed to get build info, error: %s", err)
			}

			if status[pipelineId] != pipeline.StatusText {
				statusChangeCallback(build)
				status[pipelineId] = pipeline.StatusText
			}

			if pipeline.IsRunning() {
				running++
				continue
			}

			if pipeline.IsFailed() || pipeline.IsAborted() {
				failed = true
			}

			pipelineIds = remove(pipelineIds, pipelineId)
		}
		if running == 0 {
			break
		}
		time.Sleep(time.Second * 3)
	}
	if failed {
		return fmt.Errorf("at least one build failed or aborted")
	}
	return nil
}

func remove(slice []string, what string) (b []string) {
	for _, s := range slice {
		if s != what {
			b = append(b, s)
		}
	}
	return
}
