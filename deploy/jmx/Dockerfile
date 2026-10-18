# JMX → Prometheus exporter (standalone HTTP-сервер) для Kafka и Debezium.
# Свой образ вместо sscaling/jmx-prometheus-exporter: тот собран только под amd64
# и на arm64-хосте с 16K-страницами падает под эмуляцией.
FROM eclipse-temurin:21-jre

ARG JMX_EXPORTER_VERSION=0.20.0
ADD https://repo1.maven.org/maven2/io/prometheus/jmx/jmx_prometheus_httpserver/${JMX_EXPORTER_VERSION}/jmx_prometheus_httpserver-${JMX_EXPORTER_VERSION}.jar /opt/jmx_exporter/jmx_prometheus_httpserver.jar

EXPOSE 5556
ENTRYPOINT ["java", "-jar", "/opt/jmx_exporter/jmx_prometheus_httpserver.jar", "5556", "/opt/jmx_exporter/config.yml"]
