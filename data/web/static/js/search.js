function search(url, before, after) {
    let search = document.getElementById("search").value;

    const params = new URLSearchParams({
        search: search,
    });
    if (before && before !== "") {
        params.set("before", before);
    }
    if (after && after !== "") {
        params.set("after", after);
    }

    window.location.href = url + "?" + params.toString();
}