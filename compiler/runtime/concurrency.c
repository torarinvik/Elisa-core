#include <stdint.h>
#include <stdlib.h>
#include <time.h>

#if defined(_WIN32)
#define WIN32_LEAN_AND_MEAN
#include <process.h>
#include <windows.h>
#else
#include <pthread.h>
#include <sched.h>
#endif

#if defined(__clang__) || defined(__GNUC__)
#define CTX_RUNTIME_WEAK __attribute__((weak))
#else
#define CTX_RUNTIME_WEAK
#endif

typedef void *(*ctx_entry_fn)(void *);

typedef struct {
    void *handle;
} ctx_mutex;

typedef struct {
    void *handle;
} ctx_condvar;

#if defined(_WIN32)
typedef struct {
    ctx_entry_fn entry;
    void *arg;
} ctx_thread_start;

static unsigned __stdcall ctx_thread_entry(void *raw) {
    ctx_thread_start *start = (ctx_thread_start *)raw;
    ctx_entry_fn entry = start->entry;
    void *arg = start->arg;
    free(start);
    (void)entry(arg);
    return 0u;
}
#endif

static void *ctx_xmalloc(size_t size) {
    void *ptr = malloc(size);
    if (ptr == NULL) {
        abort();
    }
    return ptr;
}

CTX_RUNTIME_WEAK int ctx_thread_create(uintptr_t *out, void *entry, void *arg) {
    if (out == NULL || entry == NULL) {
        return -1;
    }

#if defined(_WIN32)
    ctx_thread_start *start = (ctx_thread_start *)ctx_xmalloc(sizeof(*start));
    uintptr_t handle;
    start->entry = (ctx_entry_fn)entry;
    start->arg = arg;
    handle = _beginthreadex(NULL, 0u, ctx_thread_entry, start, 0u, NULL);
    if (handle == 0u) {
        free(start);
        return -1;
    }
    *out = handle;
    return 0;
#else
    pthread_t thread;
    int status = pthread_create(&thread, NULL, (ctx_entry_fn)entry, arg);
    if (status != 0) {
        return status;
    }
    *out = (uintptr_t)thread;
    return 0;
#endif
}

CTX_RUNTIME_WEAK int ctx_thread_join(uintptr_t handle) {
#if defined(_WIN32)
    if (handle == 0u) {
        return -1;
    }
    if (WaitForSingleObject((HANDLE)handle, INFINITE) != WAIT_OBJECT_0) {
        return -1;
    }
    CloseHandle((HANDLE)handle);
    return 0;
#else
    return pthread_join((pthread_t)handle, NULL);
#endif
}

CTX_RUNTIME_WEAK int ctx_thread_detach(uintptr_t handle) {
#if defined(_WIN32)
    if (handle == 0u) {
        return -1;
    }
    CloseHandle((HANDLE)handle);
    return 0;
#else
    return pthread_detach((pthread_t)handle);
#endif
}

CTX_RUNTIME_WEAK void mutex_init(ctx_mutex *out) {
    if (out == NULL) {
        abort();
    }

#if defined(_WIN32)
    CRITICAL_SECTION *mutex = (CRITICAL_SECTION *)ctx_xmalloc(sizeof(*mutex));
    InitializeCriticalSection(mutex);
    out->handle = mutex;
#else
    pthread_mutex_t *mutex = (pthread_mutex_t *)ctx_xmalloc(sizeof(*mutex));
    if (pthread_mutex_init(mutex, NULL) != 0) {
        free(mutex);
        abort();
    }
    out->handle = mutex;
#endif
}

CTX_RUNTIME_WEAK void mutex_destroy(ctx_mutex *mutex) {
    if (mutex == NULL || mutex->handle == NULL) {
        return;
    }
#if defined(_WIN32)
    DeleteCriticalSection((CRITICAL_SECTION *)mutex->handle);
#else
    if (pthread_mutex_destroy((pthread_mutex_t *)mutex->handle) != 0) {
        abort();
    }
#endif
    free(mutex->handle);
    mutex->handle = NULL;
}

CTX_RUNTIME_WEAK int ctx_mutex_lock(void *handle) {
    if (handle == NULL) {
        return -1;
    }
#if defined(_WIN32)
    EnterCriticalSection((CRITICAL_SECTION *)handle);
    return 0;
#else
    return pthread_mutex_lock((pthread_mutex_t *)handle);
#endif
}

CTX_RUNTIME_WEAK int ctx_mutex_unlock(void *handle) {
    if (handle == NULL) {
        return -1;
    }
#if defined(_WIN32)
    LeaveCriticalSection((CRITICAL_SECTION *)handle);
    return 0;
#else
    return pthread_mutex_unlock((pthread_mutex_t *)handle);
#endif
}

CTX_RUNTIME_WEAK void condvar_init(ctx_condvar *out) {
    if (out == NULL) {
        abort();
    }

#if defined(_WIN32)
    CONDITION_VARIABLE *cond = (CONDITION_VARIABLE *)ctx_xmalloc(sizeof(*cond));
    InitializeConditionVariable(cond);
    out->handle = cond;
#else
    pthread_cond_t *cond = (pthread_cond_t *)ctx_xmalloc(sizeof(*cond));
    if (pthread_cond_init(cond, NULL) != 0) {
        free(cond);
        abort();
    }
    out->handle = cond;
#endif
}

CTX_RUNTIME_WEAK void condvar_destroy(ctx_condvar *cond) {
    if (cond == NULL || cond->handle == NULL) {
        return;
    }
#if !defined(_WIN32)
    if (pthread_cond_destroy((pthread_cond_t *)cond->handle) != 0) {
        abort();
    }
#endif
    free(cond->handle);
    cond->handle = NULL;
}

CTX_RUNTIME_WEAK int ctx_cond_wait(void *cond, void *mutex) {
    if (cond == NULL || mutex == NULL) {
        return -1;
    }
#if defined(_WIN32)
    return SleepConditionVariableCS((CONDITION_VARIABLE *)cond, (CRITICAL_SECTION *)mutex, INFINITE) ? 0 : -1;
#else
    return pthread_cond_wait((pthread_cond_t *)cond, (pthread_mutex_t *)mutex);
#endif
}

CTX_RUNTIME_WEAK int ctx_cond_signal(void *cond) {
    if (cond == NULL) {
        return -1;
    }
#if defined(_WIN32)
    WakeConditionVariable((CONDITION_VARIABLE *)cond);
    return 0;
#else
    return pthread_cond_signal((pthread_cond_t *)cond);
#endif
}

CTX_RUNTIME_WEAK int ctx_cond_broadcast(void *cond) {
    if (cond == NULL) {
        return -1;
    }
#if defined(_WIN32)
    WakeAllConditionVariable((CONDITION_VARIABLE *)cond);
    return 0;
#else
    return pthread_cond_broadcast((pthread_cond_t *)cond);
#endif
}

CTX_RUNTIME_WEAK void thread_yield(void) {
#if defined(_WIN32)
    SwitchToThread();
#else
    sched_yield();
#endif
}

CTX_RUNTIME_WEAK void thread_sleep_usec(uint64_t usec) {
#if defined(_WIN32)
    DWORD millis = (DWORD)((usec + 999u) / 1000u);
    Sleep(millis);
#else
    struct timespec requested;
    requested.tv_sec = (time_t)(usec / 1000000u);
    requested.tv_nsec = (long)((usec % 1000000u) * 1000u);
    while (nanosleep(&requested, &requested) != 0) {
    }
#endif
}
