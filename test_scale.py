import requests
import threading
import time

def submit_job(i):
    payload = {"code": "import time; time.sleep(2); print(f'Job {i} done')"}
    try:
        res = requests.post("http://localhost:8080/submit", json=payload)
        job_id = res.json()['job_id']
        print(f"Job {i} submitted -> ID: {job_id}")
    except Exception as e:
        print(f"Job {i} failed: {e}")

print("🚀 Firing 10 jobs...")
threads = []
for i in range(10):
    t = threading.Thread(target=submit_job, args=(i,))
    threads.append(t)
    t.start()

for t in threads:
    t.join()
print("✅ All jobs submitted!")