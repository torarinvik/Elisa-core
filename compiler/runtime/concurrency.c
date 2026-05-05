#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>

typedef void *(*ctx_entry_fn)(void *);

typedef struct {
    uintptr_t handle;
    void *state;
} ctx_task_raw;

typedef struct {
    void *handle;
} ctx_thread_pool;

typedef struct {
    void *handle;
    void *cleanup;
} ctx_task_group;

typedef struct ctx_task_state {
    pthread_t thread;
    ctx_entry_fn entry;
    void *arg;
    void *result;
    int joined;
    struct ctx_task_state *next;
} ctx_task_state;

typedef struct {
    uint64_t worker_count;
} ctx_pool_state;

typedef struct {
    ctx_task_state *head;
    ctx_task_state *tail;
} ctx_task_group_state;

static void *ctx_run_entry(void *raw) {
    ctx_task_state *state = (ctx_task_state *)raw;
    state->result = state->entry(state->arg);
    return state->result;
}

static void *ctx_xmalloc(size_t size) {
    void *ptr = malloc(size);
    if (ptr == NULL) {
        abort();
    }
    return ptr;
}

static ctx_task_group_state *ctx_ensure_group_state(ctx_task_group *group) {
    ctx_task_group_state *state = (ctx_task_group_state *)group->handle;
    if (state != NULL) {
        return state;
    }

    state = (ctx_task_group_state *)ctx_xmalloc(sizeof(*state));
    state->head = NULL;
    state->tail = NULL;
    group->handle = state;
    return state;
}

ctx_thread_pool pool_new(uint64_t threads) {
    ctx_pool_state *state = (ctx_pool_state *)ctx_xmalloc(sizeof(*state));
    state->worker_count = threads;
    return (ctx_thread_pool){.handle = state};
}

void pool_shutdown(ctx_thread_pool *pool) {
    free(pool->handle);
    pool->handle = NULL;
}

ctx_task_raw pool_submit_raw(ctx_thread_pool *pool, void *entry, void *arg) {
    (void)pool;

    ctx_task_state *state = (ctx_task_state *)ctx_xmalloc(sizeof(*state));
    state->entry = (ctx_entry_fn)entry;
    state->arg = arg;
    state->result = NULL;
    state->joined = 0;
    state->next = NULL;
    if (pthread_create(&state->thread, NULL, ctx_run_entry, state) != 0) {
        abort();
    }

    return (ctx_task_raw){.handle = (uintptr_t)state, .state = NULL};
}

void *pool_await_raw(ctx_task_raw task) {
    ctx_task_state *state = (ctx_task_state *)(uintptr_t)task.handle;
    void *result = NULL;
    if (state == NULL) {
        return NULL;
    }

    if (!state->joined) {
        if (pthread_join(state->thread, &result) != 0) {
            abort();
        }
        state->joined = 1;
        state->result = result;
    }

    result = state->result;
    free(state);
    return result;
}

ctx_task_group task_group_new_raw(void) {
    return (ctx_task_group){.handle = NULL, .cleanup = NULL};
}

void task_group_add_raw(ctx_task_group *group, ctx_task_raw task) {
    ctx_task_state *task_state = (ctx_task_state *)(uintptr_t)task.handle;
    ctx_task_group_state *group_state;
    if (task_state == NULL) {
        return;
    }

    group_state = ctx_ensure_group_state(group);
    task_state->next = NULL;
    if (group_state->tail == NULL) {
        group_state->head = task_state;
        group_state->tail = task_state;
        return;
    }

    group_state->tail->next = task_state;
    group_state->tail = task_state;
}

void task_group_wait_all_raw(ctx_task_group *group) {
    ctx_task_group_state *group_state = (ctx_task_group_state *)group->handle;
    ctx_task_state *current;

    if (group_state == NULL) {
        return;
    }

    current = group_state->head;
    while (current != NULL) {
        ctx_task_state *next = current->next;
        void *result = NULL;
        if (!current->joined) {
            if (pthread_join(current->thread, &result) != 0) {
                abort();
            }
            current->joined = 1;
            current->result = result;
        }
        free(current);
        current = next;
    }

    free(group_state);
    group->handle = NULL;
}