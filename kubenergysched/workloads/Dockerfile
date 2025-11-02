FROM python:3.11-slim
RUN pip install kubernetes
WORKDIR /app
COPY workloads/make_jobs.py /app/make_jobs.py
ENTRYPOINT ["python","/app/make_jobs.py"]
