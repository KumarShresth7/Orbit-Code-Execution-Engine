# Orbit — Distributed Code Execution Engine 🚀

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-24.0+-2496ED?logo=docker&logoColor=white)
![Redis](https://img.shields.io/badge/Redis-Queue-DC382D?logo=redis&logoColor=white)
![FastAPI](https://img.shields.io/badge/FastAPI-Nexus_AI-009688?logo=fastapi&logoColor=white)
![Prometheus](https://img.shields.io/badge/Prometheus-Monitoring-E6522C?logo=prometheus&logoColor=white)

**Orbit** is a high-performance, distributed code execution engine designed to securely run untrusted code in isolated sandboxes. It features an integrated **AI Debugging Layer (Nexus)** that automatically diagnoses runtime errors using LLMs (Google Gemini), providing actionable feedback alongside standard execution verdicts.

---

## 🏗️ System Architecture

The system follows a producer-consumer microservices architecture orchestrated via Docker Compose.

```mermaid
graph TD
    Client[Client / Tests] -->|POST /submit| API[Go API Server]
    API -->|Push Job| Redis[(Redis Queue)]
    
    subgraph "Worker Pool (Go)"
        Worker1[Worker 1]
        Worker2[Worker 2]
        WorkerN[Worker N]
    end
    
    Redis -->|Pop Job| Worker1
    Redis -->|Pop Job| Worker2
    
    subgraph "Execution Layer"
        Sandbox[Docker Container (Python:Alpine)]
    end
    
    Worker1 -->|Execute| Sandbox
    
    subgraph "Nexus AI Service"
        Nexus[FastAPI + LangChain]
        Gemini[Google Gemini API]
    end
    
    Sandbox -->|Runtime Error?| Nexus
    Nexus -->|Analyze Traceback| Gemini
    Gemini -->|Fix & Explain| Nexus
    Nexus -->|Diagnosis| Worker1
    
    Worker1 -->|Update Result| Redis
    Client -->|GET /status| API
    
    subgraph "Observability"
        Prometheus[Prometheus] -->|Scrape Metrics| API
        Grafana[Grafana] -->|Visualize| Prometheus
    end
