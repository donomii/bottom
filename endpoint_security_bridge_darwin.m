//go:build darwin && cgo && endpointsecurity

#include "endpoint_security_bridge_darwin.h"

#include <EndpointSecurity/EndpointSecurity.h>
#include <bsm/libbsm.h>
#include <stdlib.h>
#include <string.h>

struct bottom_es_client {
	es_client_t *client;
};

static void bottom_copy_token(char *destination, size_t capacity, es_string_token_t token) {
	if (capacity == 0 || token.data == NULL || token.length == 0) {
		return;
	}
	size_t length = token.length < capacity - 1 ? token.length : capacity - 1;
	memcpy(destination, token.data, length);
	destination[length] = '\0';
}

static void bottom_append_token(char *destination, size_t capacity, es_string_token_t token) {
	size_t used = strnlen(destination, capacity);
	if (used >= capacity - 1 || token.data == NULL || token.length == 0) {
		return;
	}
	if (used > 0) {
		destination[used++] = ' ';
	}
	size_t remaining = capacity - used - 1;
	size_t length = token.length < remaining ? token.length : remaining;
	memcpy(destination + used, token.data, length);
	destination[used + length] = '\0';
}

static void bottom_copy_process(bottom_es_event *event, const es_process_t *process, uint32_t message_version) {
	event->pid = (uint32_t)audit_token_to_pid(process->audit_token);
	event->parent_pid = (uint32_t)process->ppid;
	event->uid = (uint32_t)audit_token_to_euid(process->audit_token);
	event->session_id = (uint32_t)process->session_id;
	event->pid_version = (uint32_t)audit_token_to_pidversion(process->audit_token);
	bottom_copy_token(event->executable, sizeof(event->executable), process->executable->path);
	if (message_version >= 2 && process->tty != NULL) {
		bottom_copy_token(event->tty, sizeof(event->tty), process->tty->path);
	}
	if (message_version >= 3) {
		event->start_time_unix_nano = (int64_t)process->start_time.tv_sec * 1000000000LL + process->start_time.tv_usec * 1000LL;
	}
}

static void bottom_deliver(uint64_t context, const es_message_t *message) {
	bottom_es_event event = {0};
	event.event_time_unix_nano = (int64_t)message->time.tv_sec * 1000000000LL + message->time.tv_nsec;
	event.sequence = message->version >= 4 ? message->global_seq_num : message->seq_num;
	event.global_sequence = message->version >= 4;
	switch (message->event_type) {
	case ES_EVENT_TYPE_NOTIFY_FORK:
		event.kind = BOTTOM_ES_EVENT_FORK;
		bottom_copy_process(&event, message->event.fork.child, message->version);
		bottom_copy_token(event.command, sizeof(event.command), message->event.fork.child->executable->path);
		break;
	case ES_EVENT_TYPE_NOTIFY_EXEC:
		event.kind = BOTTOM_ES_EVENT_EXEC;
		bottom_copy_process(&event, message->event.exec.target, message->version);
		for (uint32_t index = 0; index < es_exec_arg_count(&message->event.exec); index++) {
			bottom_append_token(event.command, sizeof(event.command), es_exec_arg(&message->event.exec, index));
		}
		if (message->version >= 3 && message->event.exec.cwd != NULL) {
			bottom_copy_token(event.cwd, sizeof(event.cwd), message->event.exec.cwd->path);
		}
		break;
	case ES_EVENT_TYPE_NOTIFY_EXIT:
		event.kind = BOTTOM_ES_EVENT_EXIT;
		bottom_copy_process(&event, message->process, message->version);
		event.exit_status = message->event.exit.stat;
		bottom_copy_token(event.command, sizeof(event.command), message->process->executable->path);
		break;
	default:
		return;
	}
	bottomGoESEvent(context, &event);
}

bottom_es_client *bottom_es_open(uint64_t context, int *result) {
	bottom_es_client *wrapper = calloc(1, sizeof(bottom_es_client));
	if (wrapper == NULL) {
		*result = ES_NEW_CLIENT_RESULT_ERR_INTERNAL;
		return NULL;
	}
	*result = es_new_client(&wrapper->client, ^(es_client_t *client, const es_message_t *message) {
		(void)client;
		bottom_deliver(context, message);
	});
	if (*result != ES_NEW_CLIENT_RESULT_SUCCESS) {
		free(wrapper);
		return NULL;
	}
	es_event_type_t events[] = {
		ES_EVENT_TYPE_NOTIFY_FORK,
		ES_EVENT_TYPE_NOTIFY_EXEC,
		ES_EVENT_TYPE_NOTIFY_EXIT,
	};
	if (es_subscribe(wrapper->client, events, sizeof(events) / sizeof(events[0])) != ES_RETURN_SUCCESS) {
		es_delete_client(wrapper->client);
		free(wrapper);
		*result = BOTTOM_ES_SUBSCRIBE_FAILED;
		return NULL;
	}
	return wrapper;
}

int bottom_es_close(bottom_es_client *wrapper) {
	if (wrapper == NULL) {
		return ES_RETURN_SUCCESS;
	}
	int result = es_delete_client(wrapper->client);
	free(wrapper);
	return result;
}
