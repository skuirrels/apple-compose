FROM python:3.12-alpine
ARG BANNER=default
ENV BANNER=$BANNER
WORKDIR /app
COPY server.py .
CMD ["python3", "-u", "server.py"]
