'use strict'

// Wires up a checkbox-select "Delete selected" bulk-delete form: a select-all checkbox in the
// header, one checkbox per row (checkboxClass), and a bulk submit button (bulkBtnId) that stays
// disabled until at least one row is checked. Shared across every dashboard page with this
// pattern (Portfolio's Positions and Transaction History tables, Watchlist) so the wiring only
// exists once. Per-row "Remove" buttons in the same form use their own formaction/onclick
// confirm and submit unconditionally — only the bulk button's own submit is intercepted here to
// show a count-aware confirm dialog (formId's data-confirm-bulk attribute, a %d-templated
// translated string).
// Returns the refresh function so a page that also wires up atlasSetupTableTools on the same
// table can re-run it after a filter/page change — visibleBoxes() below depends on live
// display:none state, which only atlasSetupTableTools's render() changes.
function atlasSetupBulkDelete(formId, selectAllId, checkboxClass, bulkBtnId, countId) {
  const form = document.getElementById(formId)
  if (!form) return
  const selectAll = document.getElementById(selectAllId)
  const allBoxes = () => Array.from(document.querySelectorAll('.' + checkboxClass))
  // A checkbox stays checked even while its row is filtered/paged out of view — clearing it
  // would silently drop a selection the user made deliberately. "Select all" and its visual
  // checked/indeterminate state only ever act on what's actually visible, though, so it can't
  // rope in rows the user was never shown.
  const visibleBoxes = () => allBoxes().filter(cb => cb.closest('tr').style.display !== 'none')
  const bulkBtn = document.getElementById(bulkBtnId)
  const countEl = document.getElementById(countId)

  const refresh = () => {
    const checkedAll = allBoxes().filter(cb => cb.checked)
    const visible = visibleBoxes()
    const checkedVisible = visible.filter(cb => cb.checked)
    // The count/confirm-dialog total below reflects every checked row, not just the visible
    // ones — that's the true number the form is about to submit, and undercounting it here
    // would make the confirm dialog lie about how much is really being deleted.
    countEl.textContent = checkedAll.length
    bulkBtn.disabled = checkedAll.length === 0
    selectAll.checked = checkedVisible.length > 0 && checkedVisible.length === visible.length
    selectAll.indeterminate = checkedVisible.length > 0 && checkedVisible.length < visible.length
  }

  selectAll.addEventListener('change', () => {
    visibleBoxes().forEach(cb => { cb.checked = selectAll.checked })
    refresh()
  })
  allBoxes().forEach(cb => cb.addEventListener('change', refresh))

  form.addEventListener('submit', event => {
    if (document.activeElement !== bulkBtn) return
    const count = allBoxes().filter(cb => cb.checked).length
    const message = form.dataset.confirmBulk.replace('%d', count)
    if (!confirm(message)) event.preventDefault()
  })

  refresh()
  return refresh
}

// Adds a client-side text filter and page-based pagination to a table — entirely in the
// browser, no server round-trip, matching every table in Atlas at its actual scale (dozens to
// low hundreds of rows for a single-user tool, not something that needs a real backend query).
// filterInputId is an existing <input> the page already renders (its placeholder is translated
// per-template); Previous/Next controls are created and inserted after the table. onChange, if
// given, re-runs after every render() (see atlasSetupBulkDelete's return value) so a table with
// both bulk-delete and filter/pagination wired up keeps its selection count and select-all state
// in sync with what's currently visible.
function atlasSetupTableTools(tableId, filterInputId, pageSize, onChange) {
  const table = document.getElementById(tableId)
  if (!table) return
  const tbody = table.tBodies[0]
  if (!tbody) return
  const filterInput = document.getElementById(filterInputId)
  const allRows = Array.from(tbody.rows)
  let currentPage = 0

  const pager = document.createElement('div')
  pager.className = 'd-flex justify-content-between align-items-center mt-2'
  const prevBtn = document.createElement('button')
  prevBtn.type = 'button'
  prevBtn.className = 'btn btn-outline-secondary btn-sm'
  prevBtn.textContent = '‹'
  const pageLabel = document.createElement('span')
  pageLabel.className = 'text-body-secondary small'
  const nextBtn = document.createElement('button')
  nextBtn.type = 'button'
  nextBtn.className = 'btn btn-outline-secondary btn-sm'
  nextBtn.textContent = '›'
  pager.append(prevBtn, pageLabel, nextBtn)
  table.insertAdjacentElement('afterend', pager)

  const matchingRows = () => {
    const query = filterInput ? filterInput.value.trim().toLowerCase() : ''
    if (!query) return allRows
    return allRows.filter(row => row.textContent.toLowerCase().includes(query))
  }

  function render() {
    const matches = matchingRows()
    const totalPages = Math.max(1, Math.ceil(matches.length / pageSize))
    currentPage = Math.min(Math.max(currentPage, 0), totalPages - 1)
    const pageRows = new Set(matches.slice(currentPage * pageSize, (currentPage + 1) * pageSize))

    allRows.forEach(row => { row.style.display = pageRows.has(row) ? '' : 'none' })

    pageLabel.textContent = matches.length ? `${currentPage + 1} / ${totalPages}` : '0 / 0'
    prevBtn.disabled = currentPage === 0
    nextBtn.disabled = currentPage >= totalPages - 1
    pager.style.display = matches.length > pageSize ? '' : 'none'

    if (onChange) onChange()
  }

  prevBtn.addEventListener('click', () => { currentPage -= 1; render() })
  nextBtn.addEventListener('click', () => { currentPage += 1; render() })
  if (filterInput) {
    filterInput.addEventListener('input', () => { currentPage = 0; render() })
  }
  render()
}

// atlasSetupBusySubmit guards a form whose submission is slow enough to be double-fired.
// Importing a watchlist writes one row per symbol, so a second click while the first POST is in
// flight applies the whole file twice; merge mode makes that harmless, replace mode does not.
//
// Reads its labels and confirm gate from data attributes so the markup stays declarative, the
// same shape as data-confirm-bulk on the bulk-delete forms:
//
//   data-busy-label       text shown on the button while the request is in flight
//   data-confirm-message  optional; shown before submitting
//   data-confirm-field    optional; only confirm when this field holds data-confirm-value
//   data-confirm-value    the value that arms the confirm
//
// The confirm runs first and cancelling it leaves the button completely untouched, so a user
// who backs out of a destructive import can immediately try again.
function atlasSetupBusySubmit(formId) {
  const form = document.getElementById(formId)
  if (!form) return
  const button = form.querySelector('button[type="submit"]')
  if (!button) return

  form.addEventListener('submit', event => {
    const message = form.dataset.confirmMessage
    if (message) {
      const field = form.dataset.confirmField
      const armed = !field || (form.elements[field] && form.elements[field].value === form.dataset.confirmValue)
      if (armed && !confirm(message)) {
        event.preventDefault()
        return
      }
    }
    // Width is pinned before the label changes so the button doesn't resize mid-request, which
    // reads as a layout glitch rather than as progress.
    button.style.minWidth = `${button.offsetWidth}px`
    button.disabled = true
    if (form.dataset.busyLabel) button.textContent = form.dataset.busyLabel
  })
}
