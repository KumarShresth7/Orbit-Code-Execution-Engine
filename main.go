package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/gin-gonic/gin"
)

// Define the JSON payload we expect from the user
type ExecutionRequest struct {
	Code string `json:"code"`
}

func main() {
	// 1. Initialize Gin Router
	r := gin.Default()

	// 2. Define the POST endpoint
	r.POST("/execute", func(c *gin.Context) {
		var req ExecutionRequest

		// Parse JSON body
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
			return
		}

		// Call our Docker logic
		output, err := executePythonCode(req.Code)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Return output
		c.JSON(http.StatusOK, gin.H{"output": output})
	})

	// 3. Start Server on Port 8080
	fmt.Println("🚀 Server running on http://localhost:8080")
	r.Run(":8080")
}

// executePythonCode spins up a Docker container to run the user's code
func executePythonCode(pythonCode string) (string, error) {
	ctx := context.Background()

	// A. Create Temp File
	cwd, _ := os.Getwd()
	tempDir := filepath.Join(cwd, "temp-jobs")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp dir: %v", err)
	}

	fileName := fmt.Sprintf("job_%d.py", time.Now().UnixNano()) // Unique filename
	filePath := filepath.Join(tempDir, fileName)
	if err := os.WriteFile(filePath, []byte(pythonCode), 0644); err != nil {
		return "", fmt.Errorf("failed to write code file: %v", err)
	}
	defer os.Remove(filePath) // Cleanup file after run

	// B. Connect to Docker
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithVersion("1.45"))
	if err != nil {
		return "", fmt.Errorf("docker client error: %v", err)
	}

	// C. Create Container
	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:           "python:alpine",
		Cmd:             []string{"python", "/app/" + fileName}, // Run the specific file
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
			Memory: 128 * 1024 * 1024, // 128MB Limit
		},
	}, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("container create error: %v", err)
	}

	// D. Start Container
	if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("container start error: %v", err)
	}

	// E. Wait for Finish (with Timeout)
	statusCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	var outputString string

	select {
	case err := <-errCh:
		if err != nil {
			return "", err
		}
	case <-statusCh:
		// Success
	case <-time.After(2 * time.Second): // 2s Timeout
		cli.ContainerKill(ctx, resp.ID, "SIGKILL")
		outputString = "⚠️ Error: Time Limit Exceeded"
	}

	// F. Read Logs
	out, _ := cli.ContainerLogs(ctx, resp.ID, types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	stdOutBuf := new(bytes.Buffer)
	stdErrBuf := new(bytes.Buffer)
	stdcopy.StdCopy(stdOutBuf, stdErrBuf, out)

	finalOutput := stdOutBuf.String()
	if stdErrBuf.Len() > 0 {
		finalOutput += "\n[Error Output]:\n" + stdErrBuf.String()
	}

	if outputString != "" { // Append timeout message if exists
		finalOutput += "\n" + outputString
	}

	// G. Cleanup Container
	cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})

	return finalOutput, nil
}