package main

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v6/go/gcp/logging"
	"github.com/pulumi/pulumi-gcp/sdk/v6/go/gcp/organizations"
	"github.com/pulumi/pulumi-gcp/sdk/v6/go/gcp/pubsub"
	"github.com/pulumi/pulumi-gcp/sdk/v6/go/gcp/storage"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// Handle config
		conf := config.New(ctx, "")
		benthosServiceAccountEmail := conf.Get("benthos_service_account")
		// Figure out project number
		projectInfo, err := organizations.LookupProject(ctx, nil)
		if err != nil {
			return err
		}
		projectNumber := projectInfo.Number
		// Email account for project Pub/Sub service account
		pubsubProjectServiceAccountEmail := fmt.Sprintf("service-%s@gcp-sa-pubsub.iam.gserviceaccount.com", projectNumber)
		// Create dead letter topic
		deadLetterTopicArgs := pubsub.TopicArgs{
			Name: pulumi.String("bucket-notification-dl-topic"),
		}
		deadLetterTopic, err := pubsub.NewTopic(ctx, "bucket-notification-dl-topic", &deadLetterTopicArgs)
		if err != nil {
			return err
		}
		pubsubServiceAccount := pulumi.Sprintf("serviceAccount:%s", pubsubProjectServiceAccountEmail)
		// Grant Pub/Sub service account publish role on dead letter topic
		deadLetterTopicIamMemberArgs := pubsub.TopicIAMMemberArgs{
			Member: pubsubServiceAccount,
			Role:   pulumi.String("roles/pubsub.publisher"),
			Topic:  deadLetterTopic.Name,
		}
		_, err = pubsub.NewTopicIAMMember(ctx, "notification-dl-topic-iam", &deadLetterTopicIamMemberArgs)
		if err != nil {
			return err
		}
		// Create dead letter subscription
		deadLetterSubscriptionArgs := pubsub.SubscriptionArgs{
			Topic:              deadLetterTopic.Name,
			AckDeadlineSeconds: pulumi.Int(60),
		}
		_, err = pubsub.NewSubscription(ctx, "bucket-notification-dl-sub", &deadLetterSubscriptionArgs)
		if err != nil {
			return err
		}
		// Create Pub/Sub topic for bucket event notifications
		topicArgs := pubsub.TopicArgs{
			Name: pulumi.String("bucket-notification-topic"),
		}
		topic, err := pubsub.NewTopic(ctx, "bucket-notification-topic", &topicArgs)
		if err != nil {
			return err
		}
		// Create Pub/Sub subscription for bucket event notifications
		deadLetterPolicyArgs := pubsub.SubscriptionDeadLetterPolicyArgs{
			DeadLetterTopic:     deadLetterTopic.ID(),
			MaxDeliveryAttempts: pulumi.Int(5),
		}
		subscriptionArgs := pubsub.SubscriptionArgs{
			Topic:              topic.Name,
			AckDeadlineSeconds: pulumi.Int(60),
			DeadLetterPolicy:   &deadLetterPolicyArgs,
		}
		subscription, err := pubsub.NewSubscription(ctx, "bucket-notification-sub", &subscriptionArgs)
		if err != nil {
			return err
		}

		// Grant Pub/Sub service account subscriber role on subscription
		deadLetterSubscriptionIamMemberArgs := pubsub.SubscriptionIAMMemberArgs{
			Member:       pubsubServiceAccount,
			Role:         pulumi.String("roles/pubsub.subscriber"),
			Subscription: subscription.Name,
		}
		_, err = pubsub.NewSubscriptionIAMMember(ctx, "pubsub-notification-subscription-iam", &deadLetterSubscriptionIamMemberArgs)
		if err != nil {
			return err
		}

		// Figure out Cloud Storage service account for project
		gcsAccount, err := storage.GetProjectServiceAccount(ctx, nil)
		if err != nil {
			return err
		}
		gcsServiceAccount := pulumi.Sprintf("serviceAccount:%s", gcsAccount.Id)
		// Grant GCS service account publish role on topic
		topicIamMemberArgs := pubsub.TopicIAMMemberArgs{
			Member: gcsServiceAccount,
			Role:   pulumi.String("roles/pubsub.publisher"),
			Topic:  topic.Name,
		}

		topicIam, err := pubsub.NewTopicIAMMember(ctx, "notification-topic-iam", &topicIamMemberArgs)
		if err != nil {
			return err
		}

		// TODO: Set bucket for uniform access control
		// Create bucket for logs
		bucketArgs := storage.BucketArgs{
			Location: pulumi.String("US"),
		}
		bucket, err := storage.NewBucket(ctx, "benthos-log", &bucketArgs)
		if err != nil {
			return err
		}

		// Notification
		notificationArgs := storage.NotificationArgs{
			Bucket:        bucket.Name,
			EventTypes:    pulumi.StringArray{pulumi.String("OBJECT_FINALIZE")},
			PayloadFormat: pulumi.String("JSON_API_V1"),
			Topic:         topic.Name, // //pubsub.googleapis.com/projects/{project-identifier}/topics/{my-topic}
		}
		_, err = storage.NewNotification(ctx, "bucket-notification", &notificationArgs, pulumi.DependsOn([]pulumi.Resource{topicIam}))
		if err != nil {
			return err
		}
		bucketDestination := pulumi.Sprintf("storage.googleapis.com/%s", bucket.Name)

		// Create log router
		sinkArgs := logging.ProjectSinkArgs{
			Destination: bucketDestination,
			Filter:      pulumi.String(`LOG_ID("cloudaudit.googleapis.com/activity") OR LOG_ID("externalaudit.googleapis.com/activity") OR LOG_ID("cloudaudit.googleapis.com/system_event") OR LOG_ID("externalaudit.googleapis.com/system_event") OR LOG_ID("cloudaudit.googleapis.com/access_transparency") OR LOG_ID("externalaudit.googleapis.com/access_transparency")`),
		}
		sink, err := logging.NewProjectSink(ctx, "benthos-log-sink", &sinkArgs)
		if err != nil {
			return err
		}

		// Grant write permission on bucket to logging identity
		bucketIamMemberArgs := storage.BucketIAMMemberArgs{
			Bucket: bucket.Name,
			Member: sink.WriterIdentity,
			Role:   pulumi.String("roles/storage.objectCreator"),
		}
		_, err = storage.NewBucketIAMMember(ctx, "logwriter-bucket-iam", &bucketIamMemberArgs)
		if err != nil {
			return err
		}

		// Grant IAM for Benthos service account
		// If Benthos service account exists in the stack config, do the needful

		if benthosServiceAccountEmail != "" {
			benthosServiceAccount := pulumi.Sprintf("serviceAccount:%s", benthosServiceAccountEmail)
			// grant IAM to bucket
			benthosBucketIamMemberArgs := storage.BucketIAMMemberArgs{
				Bucket: bucket.Name,
				Member: benthosServiceAccount,
				Role:   pulumi.String("roles/storage.objectAdmin"),
			}
			_, err = storage.NewBucketIAMMember(ctx, "benthos-bucket-iam", &benthosBucketIamMemberArgs)
			if err != nil {
				return err
			}
			// grant IAM to subscription
			benthosSubscriptionIamMemberArgs := pubsub.SubscriptionIAMMemberArgs{
				Member:       benthosServiceAccount,
				Role:         pulumi.String("roles/pubsub.subscriber"),
				Subscription: subscription.Name,
			}
			_, err := pubsub.NewSubscriptionIAMMember(ctx, "benthos-notification-subscription-iam", &benthosSubscriptionIamMemberArgs)
			if err != nil {
				return err
			}
		}

		// Exports
		ctx.Export("subscriptionId", subscription.ID())
		ctx.Export("bucketId", bucket.ID())
		return nil
	})
}
