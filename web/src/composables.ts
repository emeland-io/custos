import { ref, type Ref } from 'vue'

// useLoad runs fn now and on reload, keeping its result and error.
export function useLoad<T>(fn: () => Promise<T>) {
  const data = ref<T>() as Ref<T | undefined>
  const error = ref<string>()
  const loading = ref(false)
  async function reload() {
    loading.value = true
    try {
      data.value = await fn()
      error.value = undefined
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  }
  void reload()
  return { data, error, loading, reload }
}

// useAction wraps a mutation, keeping its error and busy state.
export function useAction() {
  const error = ref<string>()
  const busy = ref(false)
  async function run<T>(fn: () => Promise<T>): Promise<T | undefined> {
    busy.value = true
    error.value = undefined
    try {
      return await fn()
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      return undefined
    } finally {
      busy.value = false
    }
  }
  return { error, busy, run }
}
