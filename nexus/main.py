import os
import uvicorn
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from langchain_google_genai import ChatGoogleGenerativeAI
from dotenv import load_dotenv

load_dotenv()
GOOGLE_API_KEY = os.getenv("GOOGLE_API_KEY")

app = FastAPI()

llm = ChatGoogleGenerativeAI(
    model="gemini-2.5-flash",
    google_api_key=GOOGLE_API_KEY,
    temperature=0.7
)

class ErrorContext(BaseModel):
    code: str
    error: str

@app.post("/analyze")
def analyze_bug(ctx: ErrorContext):
    prompt = f"""
    You are an expert Python debugger. The following code failed with an error.
    
    [CODE]
    {ctx.code}
    [/CODE]

    [ERROR]
    {ctx.error}
    [/ERROR]

    Task:
    1. Identify the bug.
    2. Provide the CORRECTED code.
    3. Explain the fix in 1 short sentence.
    
    Output Format (Plain Text):
    [FIX] <corrected_code_here> [/FIX]
    [EXPLANATION] <explanation_here> [/EXPLANATION]
    """
    
    try:
        response = llm.invoke(prompt)
        return {"analysis": response.content}
    except Exception as e:
        return {"analysis": f"AI Brain Freeze: {str(e)}"}

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=5001)