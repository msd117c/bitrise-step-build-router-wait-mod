package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-steputils/stepconf"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-steplib/bitrise-step-build-router-start/bitrise"
)

// Config ...
type Config struct {
	AppSlug                string          `env:"BITRISE_APP_SLUG,required"`
	AccessToken            stepconf.Secret `env:"access_token,required"`
	PipelineIds            string          `env:"pipelineids,required"`
	AbortPipelinesOnFail   string          `env:"abort_on_fail"`
	IsVerboseLog           bool            `env:"verbose,required"`
}

func failf(s string, a ...interface{}) {
	log.Errorf(s, a...)
	os.Exit(1)
}

func main() {
	var cfg Config
	if err := stepconf.Parse(&cfg); err != nil {
		failf("Issue with an input: %s", err)
	}

	stepconf.Print(cfg)
	fmt.Println()

	log.SetEnableDebugLog(cfg.IsVerboseLog)

	app := bitrise.NewAppWithDefaultURL(cfg.AppSlug, string(cfg.AccessToken))

	log.Infof("Waiting for builds:")

	pipelineIds := strings.Split(cfg.PipelineIds, "\n")

	if err := app.WaitForPipelines(pipelineIds, func(pipeline bitrise.Pipeline) {
		var failReason string
		var pipelineURL = fmt.Sprintf("(https://app.bitrise.io/%s/pipelines/%s)", app.Slug, pipeline.Id)

		if pipeline.IsRunning() {
			log.Printf("- %s %s %s", pipeline.Name, pipeline.Status, pipelineURL)
		} else if pipeline.IsSuccessful() {
			log.Donef("- %s successful %s)", pipeline.Name, pipelineURL)
		} else if pipeline.IsFailed() {
			log.Errorf("- %s failed", pipeline.Name)
			failReason = "failed"
		} else if pipeline.IsAborted() {
			log.Warnf("- %s aborted", pipeline.Name)
			failReason = "aborted"
		} else if pipeline.IsAbortedWithSuccess() {
			log.Infof("- %s cancelled", pipeline.Name)
		}

		if cfg.AbortPipelinesOnFail == "yes" && (pipeline.IsAborted() || pipeline.IsFailed()) {
			for _, pipelineId := range pipelineIds {
				if pipelineId != pipeline.Id {
					abortErr := app.AbortPipeline(pipelineId, "Abort on Fail - Pipeline [https://app.bitrise.io/"+app.slug+"/pipelines/"+pipeline.Id+"] "+failReason+"\nAuto aborted by parent pipeline")
					if abortErr != nil {
						log.Warnf("failed to abort pipeline, error: %s", abortErr)
					}
					log.Donef("Pipeline " + pipelineId + " aborted due to associated pipeline failure")
				}
			}
		}
	}); err != nil {
		failf("An error occurred: %s", err)
	}
}
