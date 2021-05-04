FROM scratch
COPY aws-k8s-ebs-autoscaler /
CMD ["/aws-k8s-ebs-autoscaler"]
