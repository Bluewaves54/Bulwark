# SPDX-License-Identifier: Apache-2.0
#
# Node.js client for PKGuard e2e tests.
# Includes npm, yarn (classic), and pnpm for testing installs through the proxy.

FROM node:20-slim

# Activate package managers via corepack (bundled with Node 20).
# This avoids EEXIST conflicts from npm global installs on base image shims.
# COREPACK_ENABLE_DOWNLOAD_PROMPT=0 prevents interactive prompts in Node 20+.
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable && corepack prepare yarn@1.22.22 --activate && corepack prepare pnpm@9.15.4 --activate

# Install curl for health checks.
RUN apt-get update && apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*

# Copy test harness.
COPY test.sh /test.sh
RUN chmod +x /test.sh

# Run as non-root.
RUN useradd -m -u 1001 tester
USER tester
WORKDIR /home/tester

ENTRYPOINT ["/test.sh"]
