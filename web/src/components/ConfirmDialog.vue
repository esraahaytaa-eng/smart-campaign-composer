<script setup lang="ts">
import {
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogOverlay,
  AlertDialogPortal,
  AlertDialogRoot,
  AlertDialogTitle,
} from 'reka-ui'
import { t } from '../i18n'

defineProps<{ open: boolean; title: string; confirmLabel: string; cancelLabel?: string; danger?: boolean }>()
// confirm: the action was chosen; the parent acts and hides the dialog.
// close: cancelled (button or Escape). A plain button, not AlertDialogAction:
// that one closes first, so the parent would act on cleared state.
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
          <AlertDialogCancel class="btn">{{ cancelLabel ?? t('confirm.cancel') }}</AlertDialogCancel>
          <button type="button" class="btn" :class="danger ? 'btn-danger' : 'btn-primary'" @click="emit('confirm')">{{ confirmLabel }}</button>
        </div>
      </AlertDialogContent>
    </AlertDialogPortal>
  </AlertDialogRoot>
</template>
