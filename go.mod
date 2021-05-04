module aws-k8s-ebs-autoscaler

go 1.16

require (
	github.com/aws/aws-sdk-go v1.38.30
	github.com/kubernetes-csi/external-snapshotter/client/v4 v4.0.0 // indirect
	github.com/sirupsen/logrus v1.8.1
	k8s.io/api v0.21.0
	k8s.io/apimachinery v0.21.0
	k8s.io/client-go v0.21.0
)

replace k8s.io/client-go => k8s.io/client-go v0.21.0
