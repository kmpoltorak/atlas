'use strict'

// Wires up a checkbox-select "Delete selected" bulk-delete form: a select-all checkbox in the
// header, one checkbox per row (checkboxClass), and a bulk submit button (bulkBtnId) that stays
// disabled until at least one row is checked. Shared across every dashboard page with this
// pattern (Portfolio's Positions and Transaction History tables, Watchlist) so the wiring only
// exists once. Per-row "Remove" buttons in the same form use their own formaction/onclick
// confirm and submit unconditionally — only the bulk button's own submit is intercepted here to
// show a count-aware confirm dialog (formId's data-confirm-bulk attribute, a %d-templated
// translated string).
function atlasSetupBulkDelete(formId, selectAllId, checkboxClass, bulkBtnId, countId) {
  const form = document.getElementById(formId)
  if (!form) return
  const selectAll = document.getElementById(selectAllId)
  const rowBoxes = () => Array.from(document.querySelectorAll('.' + checkboxClass))
  const bulkBtn = document.getElementById(bulkBtnId)
  const countEl = document.getElementById(countId)

  const refresh = () => {
    const checked = rowBoxes().filter(cb => cb.checked)
    countEl.textContent = checked.length
    bulkBtn.disabled = checked.length === 0
    selectAll.checked = checked.length > 0 && checked.length === rowBoxes().length
    selectAll.indeterminate = checked.length > 0 && checked.length < rowBoxes().length
  }

  selectAll.addEventListener('change', () => {
    rowBoxes().forEach(cb => { cb.checked = selectAll.checked })
    refresh()
  })
  rowBoxes().forEach(cb => cb.addEventListener('change', refresh))

  form.addEventListener('submit', event => {
    if (document.activeElement !== bulkBtn) return
    const count = rowBoxes().filter(cb => cb.checked).length
    const message = form.dataset.confirmBulk.replace('%d', count)
    if (!confirm(message)) event.preventDefault()
  })

  refresh()
}
