function chat(url, cursor) {
    let search = document.getElementById("search").value;

    const params = new URLSearchParams({
        query: search,
    });

    window.location.href = url + "?" + params.toString();
}