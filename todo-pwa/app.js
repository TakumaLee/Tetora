document.addEventListener('DOMContentLoaded', () => {
    const todoForm = document.getElementById('todo-form');
    const todoInput = document.getElementById('todo-input');
    const todoList = document.getElementById('todo-list');

    // 從史官處讀取既有軍令
    let todos = JSON.parse(localStorage.getItem('wutodos')) || [];

    function saveAndRender() {
        localStorage.setItem('wutodos', JSON.stringify(todos));
        render();
    }

    function render() {
        todoList.innerHTML = '';
        todos.forEach((todo, index) => {
            const li = document.createElement('li');
            li.className = todo.completed ? 'completed' : '';
            li.innerHTML = `
                <span class="todo-text">${todo.text}</span>
                <div class="actions">
                    <button class="check-btn" data-index="${index}">旗</button>
                    <button class="del-btn" data-index="${index}">斬</button>
                </div>
            `;
            todoList.appendChild(li);
        });
    }

    // 新增軍令
    todoForm.addEventListener('submit', (e) => {
        e.preventDefault();
        const text = todoInput.value.trim();
        if (text) {
            todos.push({ text, completed: false });
            todoInput.value = '';
            saveAndRender();
        }
    });

    // 處理軍令完成與刪除
    todoList.addEventListener('click', (e) => {
        const index = e.target.getAttribute('data-index');
        if (e.target.classList.contains('check-btn')) {
            todos[index].completed = !todos[index].completed;
            saveAndRender();
        } else if (e.target.classList.contains('del-btn')) {
            todos.splice(index, 1);
            saveAndRender();
        }
    });

    render();
});
