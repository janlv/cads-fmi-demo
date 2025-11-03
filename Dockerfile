FROM python:3.11-slim

# System deps
RUN apt-get update && apt-get install -y --no-install-recommends unzip && rm -rf /var/lib/apt/lists/*

# Python deps
COPY requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt

# Workdir
WORKDIR /app

# Copy sources
COPY fmusrc/ ./fmusrc/
COPY orchestrator/ ./orchestrator/
COPY data/ ./data/

# Build FMUs (pythonfmu CLI)
RUN mkdir -p dist && \
    python -m pythonfmu export fmusrc/producer_fmu.py Producer dist/Producer.fmu && \
    python -m pythonfmu export fmusrc/consumer_fmu.py Consumer dist/Consumer.fmu && \
    echo 'Built FMUs to /app/dist'

# Default command: run orchestrator
CMD ["python", "orchestrator/run.py"]
