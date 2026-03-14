# SPDX-License-Identifier: Apache-2.0
#
# Python client for PKGuard e2e tests.
# Includes pip, pip3, and uv for testing PyPI package installs through the proxy.

FROM python:3.12-slim

# Install curl for health checks and uv for fast installs.
RUN apt-get update && apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir uv

# Copy test harness.
COPY test.sh /test.sh
RUN chmod +x /test.sh

# Run as non-root.
RUN useradd -m -u 1001 tester
USER tester
WORKDIR /home/tester

ENTRYPOINT ["/test.sh"]
