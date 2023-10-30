FROM prom/busybox:latest

COPY build/linux/prometheusRuleLoader /bin/prometheusRuleLoader
RUN chmod 755 /bin/prometheusRuleLoader

ENTRYPOINT	["/bin/prometheusRuleLoader"]
