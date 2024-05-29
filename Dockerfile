
FROM alpine:latest

WORKDIR /app
ADD revcatfront .
RUN mkdir ./cache
RUN mkdir ./config
RUN mkdir /opt/revcatfront
RUN mkdir ./data

COPY data/ ./data/
COPY config/ ./config/
ENTRYPOINT ["./revcatfront", "-config", "/opt/revcatfront/revcatfront.toml" ]
EXPOSE 8445
