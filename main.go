package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http" // Used for status codes (http.StatusOK)
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// --- 1. Data Structures ---
type Job struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Status    string `json:"status"` // pending, processing, completed, failed
	Output    string `json:"output"`
	CreatedAt int64  `json:"created_at"`
}

var ctx = context.Background()
var rdb *redis.Client

func main() {
	// --- 2. Connect to Redis ---
	fmt.Println("🔌 Connecting to Redis...")
	rdb = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	if _, err := rdb.Ping(ctx).Result(); err != nil {
		panic("❌ Cannot connect to Redis. Is it running? Error: " + err.Error())
	}
	fmt.Println("✅ Connected to Redis")

	// --- 3. Start Worker Pool (Concurrency = 5) ---
	// This spins up 5 parallel workers to handle jobs
	concurrency := 5
	fmt.Printf("👷 Starting %d Workers...\n", concurrency)
	for i := 1; i <= concurrency; i++ {
		go startWorker(i)
	}

	// --- 4. Start API Server ---
	r := gin.Default()

	// Endpoint: Submit Job
	r.POST("/submit", func(c *gin.Context) {
		var req struct {
			Code string `json:"code"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
			return
		}

		// Generate ID & Save to Redis
		jobID := fmt.Sprintf("%d", time.Now().UnixNano())
		job := Job{
			ID:        jobID,
			Code:      req.Code,
			Status:    "pending",
			CreatedAt: time.Now().Unix(),
		}

		jobJSON, _ := json.Marshal(job)
		rdb.Set(ctx, "job:"+jobID, jobJSON, 1*time.Hour) // Save Data
		rdb.LPush(ctx, "job_queue", jobID)             // Push to Queue

		c.JSON(http.StatusAccepted, gin.H{"job_id": jobID, "message": "Job queued", "status_url": "/status/" + jobID})
	})

	// Endpoint: Check Status
	r.GET("/status/:id", func(c *gin.Context) {
		jobID := c.Param("id")
		val, err := rdb.Get(ctx, "job:"+jobID).Result()
		if err == redis.Nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		var job Job
		json.Unmarshal([]byte(val), &job)
		c.JSON(http.StatusOK, job)
	})

	fmt.Println("🚀 Async Server running on http://localhost:8080")
	r.Run(":8080")
}

// --- 5. The Worker Logic ---
func startWorker(workerID int) {
	fmt.Printf("👷 Worker %d ready.\n", workerID)
	for {
		// Wait for a job (Blocking Pop)
		result, err := rdb.BLPop(ctx, 0*time.Second, "job_queue").Result()
		if err != nil {
			continue
		}

		jobID := result[1]
		fmt.Printf("⚡ [Worker %d] Processing Job: %s\n", workerID, jobID)

		// Fetch Job
		val, _ := rdb.Get(ctx, "job:"+jobID).Result()
		var job Job
		json.Unmarshal([]byte(val), &job)

		// Update Status -> Processing
		job.Status = "processing"
		updateJob(job)

		// RUN THE CODE (Docker)
		output, err := executePythonCode(job.Code)

		// Update Status -> Completed/Failed
		if err != nil {
			job.Status = "failed"
			job.Output = err.Error()
		} else {
			job.Status = "completed"
			job.Output = output
		}
		updateJob(job)
		fmt.Printf("✅ [Worker %d] Job %s Finished\n", workerID, jobID)
	}
}

func updateJob(job Job) {
	data, _ := json.Marshal(job)
	rdb.Set(ctx, "job:"+job.ID, data, 1*time.Hour)
}

// --- 6. The Docker Engine ---
func executePythonCode(pythonCode string) (string, error) {
	ctx := context.Background()

	// A. Create Temp File
	cwd, _ := os.Getwd()
	tempDir := filepath.Join(cwd, "temp-jobs")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp dir: %v", err)
	}
	fileName := fmt.Sprintf("job_%d.py", time.Now().UnixNano())
	filePath := filepath.Join(tempDir, fileName)
	if err := os.WriteFile(filePath, []byte(pythonCode), 0644); err != nil {
		return "", fmt.Errorf("failed to write code file: %v", err)
	}
	defer os.Remove(filePath)

	// B. Connect to Docker (WITH VERSION FIX)
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithVersion("1.45"))
	if err != nil {
		return "", fmt.Errorf("docker client error: %v", err)
	}

	// C. Create Container
	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:           "python:alpine",
		Cmd:             []string{"python", "/app/" + fileName},
		NetworkDisabled: true,
	}, &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   filePath,
				Target:   "/app/" + fileName,
				ReadOnly: true,
			},
		},
		Resources: container.Resources{
			Memory: 128 * 1024 * 1024,
		},
	}, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("container create error: %v", err)
	}

	// D. Start
	if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("container start error: %v", err)
	}

	// E. Wait
	statusCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	var outputString string
	select {
	case err := <-errCh:
		if err != nil {
			return "", err
		}
	case <-statusCh:
	case <-time.After(5 * time.Second): // 5s Timeout for async
		cli.ContainerKill(ctx, resp.ID, "SIGKILL")
		outputString = "⚠️ Error: Time Limit Exceeded"
	}

	// F. Logs
	out, _ := cli.ContainerLogs(ctx, resp.ID, types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	stdOutBuf := new(bytes.Buffer)
	stdErrBuf := new(bytes.Buffer)
	stdcopy.StdCopy(stdOutBuf, stdErrBuf, out)

	finalOutput := stdOutBuf.String()
	if stdErrBuf.Len() > 0 {
		finalOutput += "\n[Error Output]:\n" + stdErrBuf.String()
	}
	if outputString != "" {
		finalOutput += "\n" + outputString
	}
    
    // G. Cleanup
    cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})

	return finalOutput, nil
}