"""CPU OpenAI-compatible embedding server for a local BGE-M3 checkpoint.

Run with:
  python embedding_server.py

The model directory is selected with BGE_MODEL_PATH and defaults to the path
used by this workstation. WeKnora can call POST /v1/embeddings from Docker via
http://host.docker.internal:8090/v1.
"""

from __future__ import annotations

import os
from typing import Any

import torch
import torch.nn.functional as F
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field
from transformers import AutoModel, AutoTokenizer


MODEL_PATH = os.getenv(
    "BGE_MODEL_PATH", r"D:\a_huse_rag\rag_qa\model\bge-m3"
)
PORT = int(os.getenv("BGE_PORT", "8090"))
HOST = os.getenv("BGE_HOST", "127.0.0.1")
MAX_LENGTH = int(os.getenv("BGE_MAX_LENGTH", "8192"))

if not os.path.isdir(MODEL_PATH):
    raise RuntimeError(f"BGE_MODEL_PATH does not exist: {MODEL_PATH}")

torch.set_num_threads(int(os.getenv("BGE_CPU_THREADS", str(max(os.cpu_count() or 2, 2)))))
tokenizer = AutoTokenizer.from_pretrained(MODEL_PATH, local_files_only=True)
model = AutoModel.from_pretrained(MODEL_PATH, local_files_only=True)
model.eval()

app = FastAPI(title="Local BGE-M3 Embeddings", version="1.0.0")


class EmbeddingRequest(BaseModel):
    input: str | list[str]
    model: str | None = None
    encoding_format: str = Field(default="float", pattern="^(float|base64)$")


@app.get("/health")
def health() -> dict[str, Any]:
    return {"status": "ok", "model": "bge-m3", "dimension": 1024, "device": "cpu"}


@app.post("/v1/embeddings")
def embeddings(request: EmbeddingRequest) -> dict[str, Any]:
    if request.encoding_format != "float":
        raise HTTPException(status_code=400, detail="only encoding_format=float is supported")
    texts = [request.input] if isinstance(request.input, str) else request.input
    if not texts or any(not isinstance(text, str) or not text.strip() for text in texts):
        raise HTTPException(status_code=400, detail="input must contain non-empty text")

    encoded = tokenizer(
        texts,
        padding=True,
        truncation=True,
        max_length=MAX_LENGTH,
        return_tensors="pt",
    )
    with torch.inference_mode():
        output = model(**encoded).last_hidden_state[:, 0]
        vectors = F.normalize(output, p=2, dim=1).cpu().tolist()

    data = [
        {"object": "embedding", "index": index, "embedding": vector}
        for index, vector in enumerate(vectors)
    ]
    token_count = int(encoded["attention_mask"].sum().item())
    return {
        "object": "list",
        "data": data,
        "model": request.model or "bge-m3",
        "usage": {"prompt_tokens": token_count, "total_tokens": token_count},
    }


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host=HOST, port=PORT, log_level="info")
