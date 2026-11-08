FROM golang:1.27.1-bookworm

COPY seedfall /usr/local/bin/seedfall
WORKDIR /work
ENTRYPOINT ["seedfall"]
CMD ["help"]
