# nexus/Dockerfile
FROM python:3.10-slim

WORKDIR /app

# Install dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy the application code
COPY . .

# Expose the port
EXPOSE 5001

# Start the server (Host 0.0.0.0 is required for Docker networking)
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "5001"]