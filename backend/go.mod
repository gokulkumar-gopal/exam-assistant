module github.com/gokulkumar/exam-assistant/backend

go 1.24.0

require (
	github.com/aws/aws-lambda-go v1.54.0
	github.com/aws/aws-sdk-go-v2 v1.47.3
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.21.12
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.70.3
	github.com/awslabs/aws-lambda-go-api-proxy v0.16.2
	github.com/oklog/ulid/v2 v2.1.2
	golang.org/x/crypto v0.45.0
)

require (
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.46.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.6 // indirect
	github.com/aws/smithy-go v1.28.5 // indirect
)
