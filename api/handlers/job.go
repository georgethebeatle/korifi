package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	apierrors "code.cloudfoundry.org/korifi/api/errors"
	"code.cloudfoundry.org/korifi/api/presenter"
	"code.cloudfoundry.org/korifi/api/routing"

	"github.com/go-logr/logr"
)

const (
	JobPath            = "/v3/jobs/{guid}"
	syncSpacePrefix    = "space.apply_manifest"
	appDeletePrefix    = "app.delete"
	orgDeletePrefix    = "org.delete"
	routeDeletePrefix  = "route.delete"
	spaceDeletePrefix  = "space.delete"
	domainDeletePrefix = "domain.delete"
	roleDeletePrefix   = "role.delete"
)

const JobResourceType = "Job"

type Job struct {
	serverURL url.URL
}

func NewJob(serverURL url.URL) *Job {
	return &Job{
		serverURL: serverURL,
	}
}

func (h *Job) get(r *http.Request) (*routing.Response, error) {
	logger := logr.FromContextOrDiscard(r.Context()).WithName("handlers.job.get")

	jobGUID := routing.URLParam(r, "guid")

	jobType, resourceGUID, match := parseJobGUID(jobGUID)

	if !match {
		return nil, apierrors.LogAndReturn(
			logger,
			apierrors.NewNotFoundError(fmt.Errorf("invalid job guid: %s", jobGUID), JobResourceType),
			"Invalid Job GUID",
		)
	}

	var jobResponse presenter.JobResponse

	switch jobType {
	case syncSpacePrefix:
		jobResponse = presenter.ForManifestApplyJob(jobGUID, resourceGUID, h.serverURL)
	case appDeletePrefix, orgDeletePrefix, spaceDeletePrefix, routeDeletePrefix, domainDeletePrefix, roleDeletePrefix:
		jobResponse = presenter.ForDeleteJob(jobGUID, jobType, h.serverURL)
	default:
		return nil, apierrors.LogAndReturn(
			logger,
			apierrors.NewNotFoundError(fmt.Errorf("invalid job type: %s", jobType), JobResourceType),
			fmt.Sprintf("Invalid Job type: %s", jobType),
		)
	}

	return routing.NewResponse(http.StatusOK).WithBody(jobResponse), nil
}

func (h *Job) UnauthenticatedRoutes() []routing.Route {
	return nil
}

func (h *Job) AuthenticatedRoutes() []routing.Route {
	return []routing.Route{
		{Method: "GET", Pattern: JobPath, Handler: h.get},
	}
}

func parseJobGUID(jobGUID string) (string, string, bool) {
	// Parse the job identifier and capture the job operation and resource name for later use
	jobOperationPattern := `([a-z_\-]+[\.][a-z_]+)`   // (e.g. app.delete, space.apply_manifest, etc.)
	resourceIdentifierPattern := `([A-Za-z0-9\-\.]+)` // (e.g. cf-space-a4cd478b-0b02-452f-8498-ce87ec5c6649, CUSTOM_ORG_ID, etc.)
	jobRegexp := regexp.MustCompile(jobOperationPattern + presenter.JobGUIDDelimiter + resourceIdentifierPattern)
	matches := jobRegexp.FindStringSubmatch(jobGUID)

	if len(matches) != 3 {
		return "", "", false
	} else {
		return matches[1], matches[2], true
	}
}
