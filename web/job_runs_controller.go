package web

import (
	"errors"
	"fmt"
	"io/ioutil"

	"github.com/asdine/storm"
	"github.com/gin-gonic/gin"
	"github.com/smartcontractkit/chainlink/logger"
	"github.com/smartcontractkit/chainlink/services"
	"github.com/smartcontractkit/chainlink/store"
	"github.com/smartcontractkit/chainlink/store/models"
)

// JobRunsController manages JobRun requests in the node.
type JobRunsController struct {
	App *services.ChainlinkApplication
}

// Index returns paginated JobRuns for a given JobSpec
// Example:
//  "<application>/specs/:SpecID/runs?size=1&page=2"
func (jrc *JobRunsController) Index(c *gin.Context) {
	id := c.Param("SpecID")
	size, page, offset, err := ParsePaginatedRequest(c.Query("size"), c.Query("page"))
	if err != nil {
		c.AbortWithError(422, err)
		return
	}
	var jrs []models.JobRun
	if count, err := jrc.App.Store.Count(&models.JobRun{}); err != nil {
		c.AbortWithError(500, fmt.Errorf("error getting count of JobRuns: %+v", err))
	} else if err := jrc.App.Store.Find("JobID", id, &jrs, storm.Limit(size), storm.Skip(offset), storm.Reverse()); err != nil {
		c.AbortWithError(500, fmt.Errorf("error getting JobRuns: %+v", err))
	} else if buffer, err := NewPaginatedResponse(*c.Request.URL, size, page, count, jrs); err != nil {
		c.AbortWithError(500, fmt.Errorf("failed to marshal document: %+v", err))
	} else {
		c.Data(200, MediaType, buffer)
	}
}

// Create starts a new Run for the requested JobSpec.
// Example:
//  "<application>/specs/:SpecID/runs"
func (jrc *JobRunsController) Create(c *gin.Context) {
	id := c.Param("SpecID")

	if j, err := jrc.App.Store.FindJob(id); err == storm.ErrNotFound {
		c.AbortWithError(404, errors.New("Job not found"))
	} else if err != nil {
		c.AbortWithError(500, err)
	} else if !j.WebAuthorized() {
		c.AbortWithError(403, errors.New("Job not available on web API, recreate with web initiator"))
	} else if data, err := getRunData(c); err != nil {
		c.AbortWithError(500, err)
	} else if jr, err := startJob(j, jrc.App.Store, data); err != nil {
		c.AbortWithError(500, err)
	} else {
		c.JSON(200, gin.H{"id": jr.ID})
	}
}

func getRunData(c *gin.Context) (models.JSON, error) {
	b, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		return models.JSON{}, err
	}
	return models.ParseJSON(b)
}

// Update allows external adapters to resume a JobRun, reporting the result of
// the task and marking it no longer pending.
// Example:
//  "<application>/runs/:RunID"
func (jrc *JobRunsController) Update(c *gin.Context) {
	id := c.Param("RunID")
	var brr models.BridgeRunResult
	if jr, err := jrc.App.Store.FindJobRun(id); err == storm.ErrNotFound {
		c.AbortWithError(404, errors.New("Job Run not found"))
	} else if err != nil {
		c.AbortWithError(500, err)
	} else if !jr.Result.Status.PendingBridge() {
		c.AbortWithError(405, errors.New("Cannot resume a job run that isn't pending"))
	} else if err := c.ShouldBindJSON(&brr); err != nil {
		c.AbortWithError(500, err)
	} else {
		executeRun(jr, jrc.App.Store, brr.RunResult)
		c.JSON(200, gin.H{"id": jr.ID})
	}
}

func startJob(j models.JobSpec, s *store.Store, body models.JSON) (models.JobRun, error) {
	i := j.InitiatorsFor(models.InitiatorWeb)[0]
	jr, err := services.BuildRun(j, i, s)
	if err != nil {
		return jr, err
	}
	executeRun(jr, s, models.RunResult{Data: body})
	return jr, nil
}

func executeRun(jr models.JobRun, s *store.Store, rr models.RunResult) {
	go func() {
		if _, err := services.ExecuteRun(jr, s, rr); err != nil {
			logger.Error("Web initiator: ", err.Error())
		}
	}()
}
