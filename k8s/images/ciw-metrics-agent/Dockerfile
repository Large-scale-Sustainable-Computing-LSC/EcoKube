FROM python:3.11-slim
RUN pip install --no-cache-dir prometheus-client psutil
WORKDIR /app
COPY exporter.py /app/exporter.py
ENV METRICS_PORT=9101 SCRAPE_INTERVAL=2 TARGET_CONTAINER=task
ENTRYPOINT ["python", "/app/exporter.py"]
