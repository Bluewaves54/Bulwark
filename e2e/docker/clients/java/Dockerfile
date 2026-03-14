# SPDX-License-Identifier: Apache-2.0
#
# Java client for PKGuard e2e tests.
# Includes Maven and Gradle for testing dependency resolution through the proxy.

FROM eclipse-temurin:21-jdk

# Install Maven.
ARG MAVEN_VERSION=3.9.6
RUN apt-get update && apt-get install -y --no-install-recommends curl unzip && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL "https://archive.apache.org/dist/maven/maven-3/${MAVEN_VERSION}/binaries/apache-maven-${MAVEN_VERSION}-bin.tar.gz" \
    | tar xz -C /opt \
    && ln -s "/opt/apache-maven-${MAVEN_VERSION}/bin/mvn" /usr/local/bin/mvn

# Install Gradle.
ARG GRADLE_VERSION=8.5
RUN curl -fsSL "https://services.gradle.org/distributions/gradle-${GRADLE_VERSION}-bin.zip" -o /tmp/gradle.zip \
    && unzip -q /tmp/gradle.zip -d /opt \
    && ln -s "/opt/gradle-${GRADLE_VERSION}/bin/gradle" /usr/local/bin/gradle \
    && rm /tmp/gradle.zip

# Copy test harness and project stubs.
COPY test.sh /test.sh
COPY projects/ /projects/
RUN chmod +x /test.sh

# Run as non-root.
RUN useradd -m -u 1001 tester && chown -R tester:tester /projects
USER tester
WORKDIR /home/tester

ENTRYPOINT ["/test.sh"]
