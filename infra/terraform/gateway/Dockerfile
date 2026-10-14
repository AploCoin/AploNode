FROM nginx:1.28.0-alpine
COPY *.template /opt/aplo/
COPY entrypoint.sh /opt/aplo/entrypoint.sh
ENTRYPOINT ["/bin/sh", "/opt/aplo/entrypoint.sh"]
