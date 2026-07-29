
# RDM OpenSearch index exports

I need to be able to export OpenSearch indexes from our RDM production instances similar to how we current archive the PostgresSQL SQL. I would like to copy those indexes to an S3 bucket. OpenSearch runs inside a container in our production RDM instances on AWS.

I need to method to capture the indexes that are useful, especially the indexes that hold the stats for RDM usage as well as the indexes needed to list RDM records.


