import requests
import time

URL = "http://localhost:8080"

def submit_test(code, expected):
    print(f"\n🚀 Submitting Code: {code}")
    print(f"🎯 Expecting: {expected}")
    
    res = requests.post(f"{URL}/submit", json={
        "code": code,
        "expected_output": expected
    })
    job_id = res.json()['job_id']
    
    while True:
        status_res = requests.get(f"{URL}/status/{job_id}").json()
        if status_res['status'] in ['completed', 'failed']:
            print(f"✅ Result: {status_res['verdict']}")
            print(f"📝 Actual Output: {status_res['actual_output'].strip()}")
            break
        time.sleep(0.5)

submit_test('print("Hello World")', 'Hello World')
submit_test('print("Wrong Answer")', 'Hello World')
submit_test('print(5 + 5)', '11')