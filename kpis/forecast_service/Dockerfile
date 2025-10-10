FROM python:3.11-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt
COPY app.py .
ENV SITES_PATH=/etc/ci-aware/sites.json
CMD ["uvicorn","app:app","--host","0.0.0.0","--port","9100"]
