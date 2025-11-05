FROM python:3.11-slim

ARG TARGETARCH

# System deps
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        unzip \
        build-essential \
        cmake \
    && rm -rf /var/lib/apt/lists/*

# Add corporate CAs (place .crt PEM files under certs/)
COPY certs/ /usr/local/share/ca-certificates/
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && update-ca-certificates

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt

# Python deps
COPY requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt

# Provide optional cached pythonfmu runtime binaries (for offline/air-gapped builds)
COPY fmu/artifacts/cache/ /tmp/pythonfmu_cache/
RUN set -eux; \
    TARGET_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/resources; \
    if [ -d /tmp/pythonfmu_cache/linux/pythonfmu_resources ]; then \
        rm -rf "$TARGET_DIR"; \
        mkdir -p "$TARGET_DIR"; \
        cp -a /tmp/pythonfmu_cache/linux/pythonfmu_resources/. "$TARGET_DIR"/; \
        if [ "$TARGETARCH" = "arm64" ] && [ -d /tmp/pythonfmu_cache/apple/pythonfmu_resources ]; then \
            cp -a /tmp/pythonfmu_cache/apple/pythonfmu_resources/. "$TARGET_DIR"/; \
        fi; \
    fi; \
    rm -rf /tmp/pythonfmu_cache || true

# Rebuild pythonfmu exporter for current architecture so the generated FMUs ship
# with native binaries (arm64 on Apple Silicon, etc.).
RUN set -eux; \
    PYFMI_EXPORT_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/pythonfmu-export; \
    cd "$PYFMI_EXPORT_DIR"; \
    chmod +x build_unix.sh; \
    ./build_unix.sh; \
    rm -rf build

# Workdir
WORKDIR /app

# Copy sources
COPY fmu/models/ ./fmu/models/
COPY orchestrator/ ./orchestrator/
COPY data/ ./data/

# Build FMUs (pythonfmu CLI)
RUN mkdir -p fmu/artifacts/build && \
    python -m pythonfmu build -f fmu/models/producer_fmu.py -d fmu/artifacts/build && \
    python -m pythonfmu build -f fmu/models/consumer_fmu.py -d fmu/artifacts/build && \
    echo 'Built FMUs to /app/fmu/artifacts/build'

# Default command: run orchestrator
CMD ["python", "orchestrator/run.py"]
