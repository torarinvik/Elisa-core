#include <pthread.h>
#include <sched.h>
#include <stdint.h>
#include <stdlib.h>
#include <time.h>

#if defined(__clang__) || defined(__GNUC__)
#define CTX_RUNTIME_WEAK __attribute__((weak))
#else
#define CTX_RUNTIME_WEAK
#endif

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
} ctx_mutex;

typedef struct {
    void *handle;
} ctx_mutex_guard;

typedef struct {
    void *handle;
} ctx_condvar;

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

CTX_RUNTIME_WEAK ctx_mutex mutex_new(void) {
    pthread_mutex_t *mutex = (pthread_mutex_t *)ctx_xmalloc(sizeof(*mutex));
    if (pthread_mutex_init(mutex, NULL) != 0) {
        free(mutex);
        abort();
    }
    return (ctx_mutex){.handle = mutex};
}

CTX_RUNTIME_WEAK void mutex_init(ctx_mutex *out) {
    if (out == NULL) {
        abort();
    }
    *out = mutex_new();
}

CTX_RUNTIME_WEAK void mutex_destroy(ctx_mutex *mutex) {
    if (mutex == NULL || mutex->handle == NULL) {
        return;
    }
    if (pthread_mutex_destroy((pthread_mutex_t *)mutex->handle) != 0) {
        abort();
    }
    free(mutex->handle);
    mutex->handle = NULL;
}

CTX_RUNTIME_WEAK ctx_mutex_guard mutex_lock(ctx_mutex *mutex) {
    if (mutex == NULL || mutex->handle == NULL) {
        abort();
    }
    if (pthread_mutex_lock((pthread_mutex_t *)mutex->handle) != 0) {
        abort();
    }
    return (ctx_mutex_guard){.handle = mutex->handle};
}

CTX_RUNTIME_WEAK void mutex_unlock(ctx_mutex_guard guard) {
    if (guard.handle == NULL) {
        abort();
    }
    if (pthread_mutex_unlock((pthread_mutex_t *)guard.handle) != 0) {
        abort();
    }
}

CTX_RUNTIME_WEAK ctx_condvar condvar_new(void) {
    pthread_cond_t *cond = (pthread_cond_t *)ctx_xmalloc(sizeof(*cond));
    if (pthread_cond_init(cond, NULL) != 0) {
        free(cond);
        abort();
    }
    return (ctx_condvar){.handle = cond};
}

CTX_RUNTIME_WEAK void condvar_init(ctx_condvar *out) {
    if (out == NULL) {
        abort();
    }
    *out = condvar_new();
}

CTX_RUNTIME_WEAK void condvar_destroy(ctx_condvar *cond) {
    if (cond == NULL || cond->handle == NULL) {
        return;
    }
    if (pthread_cond_destroy((pthread_cond_t *)cond->handle) != 0) {
        abort();
    }
    free(cond->handle);
    cond->handle = NULL;
}

CTX_RUNTIME_WEAK ctx_mutex_guard cond_wait(ctx_condvar *cond, ctx_mutex_guard guard) {
    if (cond == NULL || cond->handle == NULL || guard.handle == NULL) {
        abort();
    }
    if (pthread_cond_wait((pthread_cond_t *)cond->handle, (pthread_mutex_t *)guard.handle) != 0) {
        abort();
    }
    return guard;
}

CTX_RUNTIME_WEAK void notify_one(ctx_condvar *cond) {
    if (cond == NULL || cond->handle == NULL) {
        abort();
    }
    if (pthread_cond_signal((pthread_cond_t *)cond->handle) != 0) {
        abort();
    }
}

CTX_RUNTIME_WEAK void notify_all(ctx_condvar *cond) {
    if (cond == NULL || cond->handle == NULL) {
        abort();
    }
    if (pthread_cond_broadcast((pthread_cond_t *)cond->handle) != 0) {
        abort();
    }
}

CTX_RUNTIME_WEAK void thread_yield(void) {
    sched_yield();
}

CTX_RUNTIME_WEAK void thread_sleep_usec(uint64_t usec) {
    struct timespec requested;
    requested.tv_sec = (time_t)(usec / 1000000u);
    requested.tv_nsec = (long)((usec % 1000000u) * 1000u);
    while (nanosleep(&requested, &requested) != 0) {
    }
}

CTX_RUNTIME_WEAK ctx_thread_pool pool_new(uint64_t threads) {
    ctx_pool_state *state = (ctx_pool_state *)ctx_xmalloc(sizeof(*state));
    state->worker_count = threads;
    return (ctx_thread_pool){.handle = state};
}

CTX_RUNTIME_WEAK void pool_shutdown(ctx_thread_pool *pool) {
    free(pool->handle);
    pool->handle = NULL;
}

CTX_RUNTIME_WEAK ctx_task_raw pool_submit_raw(ctx_thread_pool *pool, void *entry, void *arg) {
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

CTX_RUNTIME_WEAK void *pool_await_raw(ctx_task_raw task) {
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

CTX_RUNTIME_WEAK ctx_task_group task_group_new_raw(void) {
    return (ctx_task_group){.handle = NULL, .cleanup = NULL};
}

CTX_RUNTIME_WEAK void task_group_add_raw(ctx_task_group *group, ctx_task_raw task) {
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

CTX_RUNTIME_WEAK void task_group_wait_all_raw(ctx_task_group *group) {
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
