# Mock Mate

A simple HTTP mocking server.

It is controlled completely via API calls and can be deployed to Cloud Run.

Uses Firestore to persist mock mappings.

There is no authorisation of any kind. Never run this service publicly
available (like with `allUsers`, `allAuthenticatedUsers`).

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

Given the complexity of things like this, you will likely want to script this
rule setting. 

## Try the Rules Above

```shell
$ curl http://localhost:8080/
Hello World

$ curl -d 'i am a foo' http://localhost:8080/re
REGEX OK

$ curl -d 'i am a bar' http://localhost:8080/re
404 page not found
```

## Record a Call

You can record a call to another service with a POST
to `/mockmate-mappings:record`. This will return the request and the actual
response. The latter can be used to create a mapping.

```shell
$ curl -d '{"scheme": "https://www.example.com", "path": "/foo"}' \
    http://localhost:8080/mockmate-mappings:record

{
	"request": {
		"method": "GET",
		"path": "/foo"
	}, 
	"response": {
		"content_type": "text/html; charset=UTF-8",
		"text_body": "... lots of html ...",
		"status_code": 200,
		"headers": {
			"Age": ["421638"],
			"Cache-Control": ["max-age=604800"],
			"Content-Type": ["text/html; charset=UTF-8"],
			"Date": ["Thu, 10 Jun 2021 21:34:32 GMT"],
			...
		}
	}
}
```

The complete record request syntax:

```json
{
  "scheme": "the protocol, host name and port",
  "method": "the method",
  "path": "the path",
  "query_params": {
    "key": [
      "value1",
      "value2"
    ],
    "key2": [
      "value3"
    ]
  },
  "text_body": "string body",
  "headers": {
    "key": [
      "value1",
      "value2"
    ],
    "key2": [
      "value3"
    ]
  }
}
```

}

## See All Rules

To see all rules, send a GET to `http://localhost:8080/mockmate-mappings`.

```shell
curl http://localhost:8080/mockmate-mappings
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
    "status_code": "integer status code (not a string)",
    "headers": {
      "key": [
        "value1",
        "value2"
      ],
      "key2": [
        "value3",
        "value4"
      ]
    }
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

## Limitations & Known Issues

* There are no checks on conflicts between rules.
* By design, there are also no checks if something makes sense in HTTP terms.
  You might need to mock a really weird service.

### ToDo's

* If a recorded call returns JSON, it is returned as (escaped) text
* Always assumes outgoing recorded call body are strings in UTF-8
