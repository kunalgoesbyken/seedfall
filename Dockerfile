FROM golang:1.27.1-bookworm

COPY seedfall /usr/local/bin/seedfall
RUN git config --system --add safe.directory /work
WORKDIR /work
ENTRYPOINT ["seedfall"]
CMD ["help"]
