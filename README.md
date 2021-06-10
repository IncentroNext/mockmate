# Mock Mate

A simple HTTP mocking server.

Uses Firestore, when available, to persist mock mappings.

## Start the Server

```shell
go run server.go

# or

make run
```

## Setting a Rule

To set a rule, POST a properly structured rule json to `/mockmate-mappings`.

```shell
# set a simple rule for '/'
curl -d '{"rule": {"path":"/"}, "response": {"text_body": "Hello World\n"}}' \
    http://localhost:8080/mockmate-mappings

# sets a rule that applies to path '/re' and checks if the string 'foo' occurs in the body.
curl -d '{"rule": {"path":"/re", "text_body_regex": ".*foo.*"}, "response": {"text_body": "REGEX OK\n"}}' \
    http://localhost:8080/mockmate-mappings
```

## Try the Rules Above

```shell
$ curl http://localhost:8080/
Hello World

$ curl -d 'i am a foo' http://localhost:8080/re
REGEX OK

$ curl -d 'i am a bar' http://localhost:8080/re
404 page not found
```

## Clear All Rules

To clear all rules, send a DELETE to `http://localhost:8080/mockmate-mappings`.

```shell
curl -X DELETE http://localhost:8080/mockmate-mappings
```

## Mapping and Rule Syntax

A mapping consist of a rule and a response. If a request matches with a rule,
its linked response is returned. If no rules match, a 404 Not Found is returned.

You can set multiple fields in a rule. These are combined with logical AND.

You can add a priority to a rule. When multiple rules match, the rule with the
highest priority wins. When there are ties, one of them will be selected at
random.

```json
{
  "update_time": "set by server",
  "rule": {
    "priority": "integer value, higher value = higher priority (not a string)",
    "methods": [
      "subset of",
      "GET",
      "POST",
      "etc etc",
      "empty = all are OK"
    ],
    "path": "url path, mutually exclusive with path_regex",
    "path_regex": "url path regex, mutually exclusive with path",
    "text_body_regex": "text body regex",
    "query_params": [
      {
        "key": "multimap of",
        "value": "query parameters and values"
      },
      {
        "key": "multimap of",
        "value": "query parameters and values"
      }
    ]
  },
  "response": {
    "content_type": "response content type header",
    "text_body": "response text body",
    "json_body": {
      "some": "json object"
    },
    "bytes_body": "response byte array",
    "status_code": "integer status code (not a string)"
  }
}
```

## Build Server

[Untested]

You can use Cloud Build to create an image.

```shell
make build-async
```

## Deploy Server

[Untested]
You can use Terraform to deploy the service.

## Limitations

There are no checks on conflicts between rules.

By design, there are also no checks if something makes sense in HTTP terms. You
might need to mock a really weird service. 
