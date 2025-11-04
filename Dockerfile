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

# Provide pre-downloaded pythonfmu runtime binaries (for offline build)
COPY platform_resources/ /tmp/platform_resources/
RUN set -eux; \
    TARGET_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/resources; \
    rm -rf "$TARGET_DIR"; \
    mkdir -p "$TARGET_DIR"; \
    cp -a /tmp/platform_resources/linux/pythonfmu_resources/. "$TARGET_DIR"/; \
    if [ "$TARGETARCH" = "arm64" ]; then \
        cp -a /tmp/platform_resources/apple/pythonfmu_resources/. "$TARGET_DIR"/; \
    fi; \
    rm -rf /tmp/platform_resources

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
COPY fmusrc/ ./fmusrc/
COPY orchestrator/ ./orchestrator/
COPY data/ ./data/

# Build FMUs (pythonfmu CLI)
RUN mkdir -p dist && \
    python -m pythonfmu build -f fmusrc/producer_fmu.py -d dist && \
    python -m pythonfmu build -f fmusrc/consumer_fmu.py -d dist && \
    echo 'Built FMUs to /app/dist'

# Default command: run orchestrator
CMD ["python", "orchestrator/run.py"]
