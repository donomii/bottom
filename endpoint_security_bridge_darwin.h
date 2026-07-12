#ifndef BOTTOM_ENDPOINT_SECURITY_BRIDGE_H
#define BOTTOM_ENDPOINT_SECURITY_BRIDGE_H

#include <stdint.h>

enum {
	BOTTOM_ES_EVENT_FORK = 1,
	BOTTOM_ES_EVENT_EXEC = 2,
	BOTTOM_ES_EVENT_EXIT = 3,
	BOTTOM_ES_SUBSCRIBE_FAILED = 100
};

typedef struct bottom_es_event {
	uint32_t kind;
	uint32_t pid;
	uint32_t parent_pid;
	uint32_t uid;
	uint32_t session_id;
	uint32_t pid_version;
	int32_t exit_status;
	uint64_t sequence;
	uint32_t global_sequence;
	int64_t event_time_unix_nano;
	int64_t start_time_unix_nano;
	char executable[4096];
	char command[8192];
	char tty[4096];
	char cwd[4096];
} bottom_es_event;

typedef struct bottom_es_client bottom_es_client;

extern void bottomGoESEvent(uint64_t context, bottom_es_event *event);
bottom_es_client *bottom_es_open(uint64_t context, int *result);
int bottom_es_close(bottom_es_client *client);

#endif
