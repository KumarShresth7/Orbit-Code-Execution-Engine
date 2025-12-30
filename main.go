package main
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)


type Job struct {
	ID             string `json:"id"`
	Code           string `json:"code"`
	ExpectedOutput string `json:"expected_output"`
	ActualOutput   string `json:"actual_output"`
	Verdict        string `json:"verdict"`     
	AiDiagnosis    string `json:"ai_diagnosis"` 
	Status         string `json:"status"`
	CreatedAt      int64  `json:"created_at"`
}


var jobsProcessed = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "orbit_jobs_processed_total",
		Help: "Total number of jobs processed",
	},
)
var aiCalls = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "orbit_ai_calls_total",
		Help: "Total number of times Nexus AI was triggered",
	},
)

func init() {
	prometheus.MustRegister(jobsProcessed)
	prometheus.MustRegister(aiCalls)
}

var ctx = context.Background()
var rdb *redis.Client

func main() {
	fmt.Println("🔌 Connecting to Redis...")
	rdb = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	if _, err := rdb.Ping(ctx).Result(); err != nil {
		fmt.Println("⚠️  Redis not found. Ensure docker-compose is up.")
		panic(err)
	}
	fmt.Println("✅ Connected to Redis")

	concurrency := 5
	fmt.Printf("👷 Starting %d Workers...\n", concurrency)
	for i := 1; i <= concurrency; i++ {
		go startWorker(i)
	}

	r := gin.Default()
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/submit", func(c *gin.Context) {
		var req struct {
			Code           string `json:"code"`
			ExpectedOutput string `json:"expected_output"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
			return
		}

		jobID := fmt.Sprintf("%d", time.Now().UnixNano())
		job := Job{
			ID:             jobID,
			Code:           req.Code,
			ExpectedOutput: req.ExpectedOutput,
			Status:         "pending",
			CreatedAt:      time.Now().Unix(),
		}

		jobJSON, _ := json.Marshal(job)
		rdb.Set(ctx, "job:"+jobID, jobJSON, 1*time.Hour)
		rdb.LPush(ctx, "job_queue", jobID)

		c.JSON(http.StatusAccepted, gin.H{"job_id": jobID, "message": "Job queued"})
	})

	r.GET("/status/:id", func(c *gin.Context) {
		jobID := c.Param("id")
		val, err := rdb.Get(ctx, "job:"+jobID).Result()
		if err == redis.Nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
			return
		}
		var job Job
		json.Unmarshal([]byte(val), &job)
		c.JSON(http.StatusOK, job)
	})

	fmt.Println("🚀 Orbit Server + Nexus AI running on http://localhost:8080")
	r.Run(":8080")
}

func startWorker(workerID int) {
	fmt.Printf("👷 Worker %d ready.\n", workerID)
	for {
		result, err := rdb.BLPop(ctx, 0*time.Second, "job_queue").Result()
		if err != nil {
			continue
		}

		jobID := result[1]
		val, _ := rdb.Get(ctx, "job:"+jobID).Result()
		var job Job
		json.Unmarshal([]byte(val), &job)

		job.Status = "processing"
		updateJob(job)

		output, err := executePythonCode(job.Code)
		job.ActualOutput = output

		isRuntimeError := false
		if err != nil {
			isRuntimeError = true 
			job.ActualOutput = err.Error()
		} else if strings.Contains(output, "Traceback (most recent call last)") || strings.Contains(output, "Error:") {
			isRuntimeError = true 
		}

		if isRuntimeError {
			job.Status = "failed"
			job.Verdict = "Runtime Error"
			
			fmt.Printf("🤖 [Worker %d] Runtime Error detected. Calling Nexus...\n", workerID)
			job.AiDiagnosis = callNexusAI(job.Code, job.ActualOutput)
			aiCalls.Inc()
		} else {
			job.Status = "completed"
			
			if strings.TrimSpace(job.ActualOutput) == strings.TrimSpace(job.ExpectedOutput) {
				job.Verdict = "Passed"
			} else {
				job.Verdict = "Failed"
			}
		}

		updateJob(job)
		jobsProcessed.Inc()
		fmt.Printf("✅ [Worker %d] Job %s -> Verdict: %s\n", workerID, jobID, job.Verdict)
	}
}
func updateJob(job Job) {
	data, _ := json.Marshal(job)
	rdb.Set(ctx, "job:"+job.ID, data, 1*time.Hour)
}

func callNexusAI(code, errorMsg string) string {
	requestBody, _ := json.Marshal(map[string]string{
		"code":  code,
		"error": errorMsg,
	})

	resp, err := http.Post("http://0.0.0.0:5001/analyze", "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return "⚠️ Nexus AI Unavailable: " + err.Error()
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	json.Unmarshal(body, &result)
	
	return result["analysis"]
}

func executePythonCode(pythonCode string) (string, error) {
	ctx := context.Background()
	cwd, _ := os.Getwd()
	tempDir := filepath.Join(cwd, "temp-jobs")
	os.MkdirAll(tempDir, 0755)
	
	fileName := fmt.Sprintf("job_%d.py", time.Now().UnixNano())
	filePath := filepath.Join(tempDir, fileName)
	os.WriteFile(filePath, []byte(pythonCode), 0644)
	defer os.Remove(filePath)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithVersion("1.45"))
	if err != nil {
		return "", fmt.Errorf("client error: %v", err)
	}

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:           "python:alpine",
		Cmd:             []string{"python", "-u", "/app/" + fileName}, 
		NetworkDisabled: true,
	}, &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: filePath, Target: "/app/" + fileName, ReadOnly: true},
		},
		Resources: container.Resources{Memory: 128 * 1024 * 1024},
	}, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create error: %v", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("start error: %v", err)
	}

	statusCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	var timeoutWarning string
	select {
	case <-statusCh:
	case <-errCh:
	case <-time.After(5 * time.Second):
		cli.ContainerKill(ctx, resp.ID, "SIGKILL")
		timeoutWarning = "\n⚠️ Time Limit Exceeded"
	}

	out, _ := cli.ContainerLogs(ctx, resp.ID, types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	stdOutBuf := new(bytes.Buffer)
	stdErrBuf := new(bytes.Buffer) 
	
	stdcopy.StdCopy(stdOutBuf, stdErrBuf, out)

	finalOutput := stdOutBuf.String()
	if stdErrBuf.Len() > 0 {
		finalOutput += "\n" + stdErrBuf.String() 
	}
	finalOutput += timeoutWarning
	
	cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})

	return finalOutput, nil
}