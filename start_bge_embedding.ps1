$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $repo
$env:BGE_MODEL_PATH = "D:\a_huse_rag\rag_qa\model\bge-m3"
$env:BGE_HOST = "0.0.0.0"
$env:BGE_PORT = "8090"
$env:BGE_CPU_THREADS = "4"
& (Join-Path $repo ".venv-bge\Scripts\python.exe") (Join-Path $repo "embedding_server.py")
