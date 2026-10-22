<script setup lang="ts">
import {
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogOverlay,
  AlertDialogPortal,
  AlertDialogRoot,
  AlertDialogTitle,
} from 'reka-ui'

defineProps<{ open: boolean; title: string; confirmLabel: string; cancelLabel?: string; danger?: boolean }>()
// confirm: the action was chosen. close: the dialog closed, by any means
// (action, cancel, Escape); the parent hides it then.
const emit = defineEmits<{ confirm: []; close: [] }>()
</script>

<template>
  <AlertDialogRoot :open="open" @update:open="(v) => !v && emit('close')">
    <AlertDialogPortal>
      <AlertDialogOverlay class="overlay" />
      <AlertDialogContent class="dialog">
        <AlertDialogTitle class="title">{{ title }}</AlertDialogTitle>
        <AlertDialogDescription as="div" class="body"><slot /></AlertDialogDescription>
        <div class="actions">
          <AlertDialogCancel class="btn">{{ cancelLabel ?? 'Cancel' }}</AlertDialogCancel>
          <AlertDialogAction class="btn" :class="danger ? 'btn-danger' : 'btn-primary'" @click="emit('confirm')">{{ confirmLabel }}</AlertDialogAction>
        </div>
      </AlertDialogContent>
    </AlertDialogPortal>
  </AlertDialogRoot>
</template>
